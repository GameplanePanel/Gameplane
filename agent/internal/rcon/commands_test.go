package rcon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
)

type recordCommand func(string) (string, error)

func (f recordCommand) Exec(command string) (string, error) { return f(command) }

func TestCommandsExcludeOtherCallersFromSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls []string
		c := NewCommands(recordCommand(func(s string) (string, error) { calls = append(calls, s); return s, nil }))
		release := make(chan struct{})
		go func() {
			err := c.Sequence(t.Context(), func(rc Executor) error {
				_, _ = rc.Exec("first")
				<-release
				_, _ = rc.Exec("second")
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
		synctest.Wait()
		go func() {
			if out, err := c.Exec("console"); out != "console" || err != nil {
				t.Errorf("console: %q, %v", out, err)
			}
		}()
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			if err := c.Sequence(ctx, func(Executor) error { t.Error("ran canceled sequence"); return nil }); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled sequence: %v", err)
			}
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !slices.Equal(calls, []string{"first"}) {
			t.Fatalf("sequence interleaved: %v", calls)
		}
		close(release)
		synctest.Wait()
		if !slices.Equal(calls, []string{"first", "second", "console"}) {
			t.Fatalf("calls=%v", calls)
		}
	})
}
