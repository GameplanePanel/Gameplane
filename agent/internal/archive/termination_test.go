package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTerminationWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	v := map[string]string{"status": "ok", "snapshot": "abc123"}
	if err := WriteTermination(path, v); err != nil {
		t.Fatalf("WriteTermination: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("termination file = %s, want %s", got, want)
	}
}

func TestWriteTerminationTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	err := WriteTermination(path, strings.Repeat("x", 5000))
	if err == nil || !strings.Contains(err.Error(), "termination message too large") {
		t.Fatalf("WriteTermination error = %v, want too large", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("termination file should not be written when too large, stat err = %v", statErr)
	}
}

func TestWriteTerminationAtLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	// A JSON string of n bytes encodes to n+2 bytes; this is exactly 4096.
	if err := WriteTermination(path, strings.Repeat("x", maxTerminationBytes-2)); err != nil {
		t.Fatalf("WriteTermination at limit: %v", err)
	}
}

func TestWriteTerminationUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "termination-log")
	if err := WriteTermination(path, "ok"); err == nil {
		t.Fatal("WriteTermination to missing directory: want error")
	}
}

func TestWriteTerminationMarshalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	if err := WriteTermination(path, make(chan int)); err == nil {
		t.Fatal("WriteTermination with unencodable value: want error")
	}
}
