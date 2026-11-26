// Package archive implements the archive worker: a short-lived process that
// exports, receives or copies game server backups on behalf of a backup Job.
package archive

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// Options holds the parsed command line of one archive worker invocation.
type Options struct {
	Mode           string
	UID            string
	Addr           string
	Cert           string
	Key            string
	ClientCA       string
	IdleTimeout    time.Duration
	TerminationLog string
}

// Main runs the archive worker and returns the process exit code. args[0] is
// the mode (export, receive or copy) and the rest are its flags.
func Main(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || !validMode(args[0]) {
		writeUsage(stderr)
		return 2
	}
	opts, err := parseFlags(args[0], args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		writeUsage(stderr)
		return 2
	}
	if opts.Mode == "export" {
		return runExport(ctx, opts, os.Getenv, stderr)
	}
	fmt.Fprintf(stderr, "%s: not implemented yet\n", opts.Mode)
	return 3
}

func validMode(mode string) bool {
	switch mode {
	case "export", "receive", "copy":
		return true
	}
	return false
}

func parseFlags(mode string, args []string) (Options, error) {
	if !validMode(mode) {
		return Options{}, fmt.Errorf("unknown mode %q", mode)
	}
	opts := Options{Mode: mode}
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.UID, "uid", "", "UID of the GameServer this worker serves")
	fs.StringVar(&opts.Addr, "addr", ":9443", "listen address")
	fs.StringVar(&opts.Cert, "cert", "", "TLS certificate file")
	fs.StringVar(&opts.Key, "key", "", "TLS private key file")
	fs.StringVar(&opts.ClientCA, "client-ca", "", "CA file that signs accepted client certificates")
	fs.DurationVar(&opts.IdleTimeout, "idle-timeout", 10*time.Minute, "exit after this long without a successful request")
	fs.StringVar(&opts.TerminationLog, "termination-log", "/dev/termination-log", "file the termination message is written to")
	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("%s: %w", mode, err)
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("%s: unexpected arguments %v", mode, fs.Args())
	}
	if opts.UID == "" {
		return Options{}, fmt.Errorf("%s: --uid is required", mode)
	}
	if opts.IdleTimeout <= 0 {
		return Options{}, fmt.Errorf("%s: --idle-timeout must be positive", mode)
	}
	return opts, nil
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: archive-worker <export|receive|copy> --uid <server-uid> "+
		"[--addr :9443] [--cert FILE --key FILE --client-ca FILE] "+
		"[--idle-timeout 10m] [--termination-log /dev/termination-log]")
}
