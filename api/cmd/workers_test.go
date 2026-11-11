package main

import (
	"flag"
	"testing"
)

func TestHAProtocolRequiresNativePostgres(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"sqlite", []string{"--db-driver=sqlite"}, true},
		{"native postgres", []string{"--db-driver=postgres", "--standalone=false", "--postgres-ha-protocol=1"}, true},
		{"old protocol", []string{"--db-driver=postgres", "--postgres-ha-protocol=0"}, false},
		{"empty protocol", []string{"--db-driver=postgres", "--postgres-ha-protocol="}, false},
		{"future protocol", []string{"--db-driver=postgres", "--postgres-ha-protocol=2"}, false},
		{"sqlite HA", []string{"--db-driver=sqlite", "--postgres-ha-protocol=1"}, false},
		{"standalone HA", []string{"--db-driver=postgres", "--standalone", "--postgres-ha-protocol=1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config
			flags := flag.NewFlagSet("serve", flag.ContinueOnError)
			cfg.bindFlags(flags)
			err := flags.Parse(tc.args)
			if err == nil {
				err = cfg.validatePostgresHA()
			}
			if (err == nil) != tc.valid {
				t.Fatalf("configuration validity=%v, err=%v", err == nil, err)
			}
		})
	}
}
