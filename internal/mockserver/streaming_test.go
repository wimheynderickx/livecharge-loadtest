package mockserver

import (
	"bytes"
	"testing"
	"time"
)

// fakeFlushWriter captures each Flush-delimited chunk plus its timestamp,
// so tests can assert both chunk boundaries and inter-chunk delay.
type fakeFlushWriter struct {
	chunks     [][]byte
	flushTimes []time.Time
	pending    bytes.Buffer
}

func (w *fakeFlushWriter) Write(p []byte) (int, error) {
	return w.pending.Write(p)
}

func (w *fakeFlushWriter) Flush() {
	w.chunks = append(w.chunks, append([]byte(nil), w.pending.Bytes()...))
	w.flushTimes = append(w.flushTimes, time.Now())
	w.pending.Reset()
}

func TestStreamResponse_SplitsIntoChunks(t *testing.T) {
	w := &fakeFlushWriter{}
	streamResponse(w, []byte("aaabbbccc"), 3, 0)
	if len(w.chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(w.chunks))
	}
	got := string(w.chunks[0]) + string(w.chunks[1]) + string(w.chunks[2])
	if got != "aaabbbccc" {
		t.Errorf("reassembled = %q", got)
	}
}

func TestStreamResponse_DelayBetweenChunks(t *testing.T) {
	w := &fakeFlushWriter{}
	streamResponse(w, []byte("abcdef"), 3, 20)
	if len(w.flushTimes) != 3 {
		t.Fatalf("expected 3 flush timestamps")
	}
	for i := 1; i < len(w.flushTimes); i++ {
		gap := w.flushTimes[i].Sub(w.flushTimes[i-1])
		if gap < 15*time.Millisecond {
			t.Errorf("gap[%d] = %v, want >= 15ms", i, gap)
		}
	}
}

func TestStreamResponse_ChunksOne_NoStreaming(t *testing.T) {
	w := &fakeFlushWriter{}
	streamResponse(w, []byte("abcdef"), 1, 100)
	if len(w.chunks) != 1 {
		t.Fatalf("chunks=1 should emit a single chunk; got %d", len(w.chunks))
	}
	if string(w.chunks[0]) != "abcdef" {
		t.Errorf("chunk = %q", string(w.chunks[0]))
	}
}
