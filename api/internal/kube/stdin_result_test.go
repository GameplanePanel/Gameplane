package kube

import (
	"context"
	"errors"
	"testing"
)

func TestStdinResult(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	open := make(chan struct{})

	if err := stdinResult(closed, context.Canceled, "ns", "p"); err != nil {
		t.Fatalf("drained + canceled = %v, want nil", err)
	}
	if err := stdinResult(closed, context.DeadlineExceeded, "ns", "p"); err != nil {
		t.Fatalf("drained + deadline = %v, want nil", err)
	}
	boom := errors.New("boom")
	if err := stdinResult(closed, boom, "ns", "p"); !errors.Is(err, boom) {
		t.Fatalf("drained + real error = %v, want wrapped boom", err)
	}
	if err := stdinResult(open, context.DeadlineExceeded, "ns", "p"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("undrained + deadline = %v, want wrapped deadline error", err)
	}
	if err := stdinResult(open, context.Canceled, "ns", "p"); err == nil {
		t.Fatal("undrained + canceled = nil, want error")
	}
	if err := stdinResult(open, nil, "ns", "p"); err == nil {
		t.Fatal("undrained + nil = nil, want error")
	}
}
