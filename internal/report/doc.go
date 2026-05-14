// Package report writes periodic CSV snapshots of scenario metrics to disk.
//
// One CSVWriter per scenario. The writer takes Snapshot() readings on its
// own ticker — it does not interfere with the engine's hot path.
//
// File semantics:
//   - "{timestamp}" in CSVPath is replaced once, at writer construction,
//     with time.Now().Format(TimestampFormat).
//   - Overwrite=true (the default) truncates the file on open.
//   - Overwrite=false opens for append. The header row is still written
//     when the file is empty.
package report
