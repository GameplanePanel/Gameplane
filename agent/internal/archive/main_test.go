package archive

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"testing"
	"time"
)

func TestMainUsageErrorsReturn2(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown mode", []string{"bogus", "--uid", "u"}},
		{"missing uid", []string{"export"}},
		{"unknown flag", []string{"export", "--nope"}},
		{"help", []string{"export", "-h"}},
		{"positional argument", []string{"export", "--uid", "u", "extra"}},
		{"zero idle timeout", []string{"export", "--uid", "u", "--idle-timeout", "0s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := Main(context.Background(), tt.args, &buf); got != 2 {
				t.Fatalf("Main exit code = %d, want 2", got)
			}
			if buf.Len() == 0 {
				t.Error("Main should print usage or an error to stderr")
			}
		})
	}
}

func TestMainValidModesNotImplemented(t *testing.T) {
	for _, mode := range []string{"export", "receive", "copy"} {
		t.Run(mode, func(t *testing.T) {
			var buf bytes.Buffer
			if got := Main(context.Background(), []string{mode, "--uid", "u"}, &buf); got != 3 {
				t.Fatalf("Main exit code = %d, want 3", got)
			}
			if want := mode + ": not implemented yet\n"; buf.String() != want {
				t.Errorf("stderr = %q, want %q", buf.String(), want)
			}
		})
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	opts, err := parseFlags("export", []string{"--uid", "server-1"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	want := Options{
		Mode:           "export",
		UID:            "server-1",
		Addr:           ":9443",
		IdleTimeout:    10 * time.Minute,
		TerminationLog: "/dev/termination-log",
	}
	if opts != want {
		t.Errorf("parseFlags defaults = %+v, want %+v", opts, want)
	}
}

func TestParseFlagsAllFlags(t *testing.T) {
	args := []string{
		"--uid", "u1",
		"--addr", ":1234",
		"--cert", "tls.crt",
		"--key", "tls.key",
		"--client-ca", "ca.crt",
		"--idle-timeout", "30s",
		"--termination-log", "term.log",
	}
	opts, err := parseFlags("receive", args)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	want := Options{
		Mode:           "receive",
		UID:            "u1",
		Addr:           ":1234",
		Cert:           "tls.crt",
		Key:            "tls.key",
		ClientCA:       "ca.crt",
		IdleTimeout:    30 * time.Second,
		TerminationLog: "term.log",
	}
	if opts != want {
		t.Errorf("parseFlags = %+v, want %+v", opts, want)
	}
}

func TestParseFlagsErrors(t *testing.T) {
	if _, err := parseFlags("bogus", []string{"--uid", "u"}); err == nil {
		t.Error("unknown mode: want error")
	}
	if _, err := parseFlags("copy", nil); err == nil {
		t.Error("missing --uid: want error")
	}
	if _, err := parseFlags("copy", []string{"--uid", "u", "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h error = %v, want flag.ErrHelp", err)
	}
}
