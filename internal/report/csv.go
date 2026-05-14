package report

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/metrics"
)

// SnapshotSource is the minimal contract CSVWriter needs from a runner.
// Defining it here keeps the package free of an engine import (and the
// resulting cycle: engine → report → engine).
type SnapshotSource interface {
	Snapshot() metrics.Snapshot
}

// CSVWriter periodically samples a SnapshotSource and appends one row per
// tick to a file.
type CSVWriter struct {
	path        string
	file        *os.File
	w           *csv.Writer
	source      SnapshotSource
	percentiles []float64
	interval    time.Duration
	done        chan struct{}
	wg          sync.WaitGroup

	// Predicate column names are discovered lazily as predicates show up
	// in snapshots. We keep them sorted so columns are stable across runs.
	mu           sync.Mutex
	predicateCols []string
	headerWritten bool
}

// NewCSVWriter resolves the {timestamp} placeholder, creates parent
// directories if needed, opens the file with the requested mode, and writes
// the header row.
func NewCSVWriter(cfg config.ReportConfig, source SnapshotSource, percentiles []float64) (*CSVWriter, error) {
	resolved := strings.ReplaceAll(cfg.CSVPath, "{timestamp}", time.Now().Format(cfg.TimestampFormat))

	if dir := filepath.Dir(resolved); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("csv: create %s: %w", dir, err)
		}
	}

	flag := os.O_CREATE | os.O_WRONLY
	if cfg.Overwrite != nil && *cfg.Overwrite {
		flag |= os.O_TRUNC
	} else {
		flag |= os.O_APPEND
	}

	f, err := os.OpenFile(resolved, flag, 0o644)
	if err != nil {
		return nil, fmt.Errorf("csv: open %s: %w", resolved, err)
	}

	cw := &CSVWriter{
		path:        resolved,
		file:        f,
		w:           csv.NewWriter(f),
		source:      source,
		percentiles: percentiles,
		interval:    cfg.FlushInterval.Duration,
		done:        make(chan struct{}),
	}
	return cw, nil
}

// Path returns the resolved file path (with {timestamp} substituted).
func (c *CSVWriter) Path() string { return c.path }

// Start launches the background sampling goroutine.
func (c *CSVWriter) Start() {
	c.wg.Add(1)
	go c.loop()
}

// Stop signals the goroutine to exit, flushes pending rows, and closes
// the file. After Stop the writer must not be reused.
func (c *CSVWriter) Stop() error {
	close(c.done)
	c.wg.Wait()
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		_ = c.file.Close()
		return err
	}
	return c.file.Close()
}

// loop is the background sampling goroutine.
func (c *CSVWriter) loop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Take an immediate snapshot so very short runs still produce data.
	c.writeRow(c.source.Snapshot())

	for {
		select {
		case <-c.done:
			// One final snapshot so the file reflects the final counters.
			c.writeRow(c.source.Snapshot())
			return
		case <-ticker.C:
			c.writeRow(c.source.Snapshot())
		}
	}
}

// writeRow serialises one snapshot. Predicate columns are reconciled with
// what we've seen before so the CSV stays rectangular.
func (c *CSVWriter) writeRow(snap metrics.Snapshot) {
	c.reconcilePredicateCols(snap)

	if !c.headerWritten {
		c.writeHeader()
		c.headerWritten = true
	}

	row := []string{
		time.Now().UTC().Format(time.RFC3339),
		snap.ScenarioName,
		strconv.FormatInt(snap.Sent, 10),
		strconv.FormatInt(snap.Received, 10),
		strconv.FormatInt(snap.Errors, 10),
		strconv.FormatFloat(snap.MsgPerSec, 'f', 2, 64),
	}
	for _, p := range c.percentiles {
		// Three-decimal ms preserves the underlying µs resolution while
		// staying parseable by spreadsheets.
		row = append(row, strconv.FormatFloat(metrics.LatencyMs(snap.Percentiles[p]), 'f', 3, 64))
	}
	c.mu.Lock()
	cols := append([]string{}, c.predicateCols...)
	c.mu.Unlock()
	for _, name := range cols {
		ps, ok := snap.Predicates[name]
		if !ok {
			row = append(row, "0")
		} else {
			row = append(row, strconv.FormatInt(ps.Count, 10))
		}
	}

	_ = c.w.Write(row)
	c.w.Flush()
}

// writeHeader composes the column list and writes it once.
func (c *CSVWriter) writeHeader() {
	header := []string{"timestamp", "scenario", "sent", "received", "errors", "msg_per_sec"}
	for _, p := range c.percentiles {
		header = append(header, fmt.Sprintf("p%s_ms", formatPercentile(p)))
	}
	c.mu.Lock()
	for _, name := range c.predicateCols {
		header = append(header, "predicate_"+name+"_count")
	}
	c.mu.Unlock()
	_ = c.w.Write(header)
	c.w.Flush()
}

// reconcilePredicateCols adds any new predicate names from the snapshot.
// Existing names are kept in their original column position so the file
// remains stable.
func (c *CSVWriter) reconcilePredicateCols(snap metrics.Snapshot) {
	if len(snap.Predicates) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	known := make(map[string]bool, len(c.predicateCols))
	for _, n := range c.predicateCols {
		known[n] = true
	}
	added := false
	for name := range snap.Predicates {
		if !known[name] {
			c.predicateCols = append(c.predicateCols, name)
			added = true
		}
	}
	if added {
		sort.Strings(c.predicateCols)
	}
}

// formatPercentile renders a percentile (e.g. 99.9) into a column-friendly
// string ("99_9"). Avoids dots in CSV headers because some downstream tools
// treat them as separators.
func formatPercentile(p float64) string {
	s := strconv.FormatFloat(p, 'f', -1, 64)
	return strings.ReplaceAll(s, ".", "_")
}
