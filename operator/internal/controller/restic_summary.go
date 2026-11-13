package controller

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ResticSummary mirrors the trailing summary line restic prints when
// run with `--json`. We only care about the snapshot id and the size,
// which is what the Backup status surfaces. restic prints many JSON
// lines per run (`status`, `verbose_status`, etc.); only the last one
// has message_type=summary, so the parser scans to the last summary
// line in the stream.
type ResticSummary struct {
	MessageType         string `json:"message_type"`
	SnapshotID          string `json:"snapshot_id"`
	TotalBytesProcessed int64  `json:"total_bytes_processed"`
	PackedBytesAdded    *int64 `json:"data_added_packed"`
}

// ErrNoResticSummary is returned by ParseResticSummary when the input
// contained no summary line. This is normal for failed jobs and
// callers should distinguish "no summary" from "decode error".
var ErrNoResticSummary = errors.New("no restic summary line in stream")

// ParseResticSummary reads logs line-by-line and returns the last JSON
// object whose message_type is "summary". Non-JSON lines and JSON
// lines with other message types (status, verbose_status) are silently
// skipped — restic mixes plain text and JSON when stderr leaks into
// pod logs, so we cannot fail the parse on malformed input.
func ParseResticSummary(r io.Reader) (*ResticSummary, error) {
	scanner := bufio.NewScanner(r)
	// Pod logs can include long lines (especially `verbose_status`
	// with file paths). Allow up to 1 MiB per line before giving up.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	var last *ResticSummary
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var s ResticSummary
		if err := json.Unmarshal(line, &s); err != nil {
			continue
		}
		if s.MessageType == "summary" {
			cp := s
			last = &cp
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read restic logs: %w", err)
	}
	if last == nil {
		return nil, ErrNoResticSummary
	}
	return last, nil
}
