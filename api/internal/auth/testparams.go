package auth

import (
	"context"
	"sync"
)

// SetFastHashParams drops the argon2id cost parameters to the cheapest
// sensible values for the duration of a test and restores them via
// tb.Cleanup. Hashing at production cost is tens of milliseconds plus a
// 64 MiB allocation per call, which multiplies across every seeded user
// and password round-trip in the api test suites. Verification is
// unaffected: VerifyPassword reads its parameters from the PHC hash
// string, so hashes made cheap verify cheap and production hashes still
// verify at full cost.
//
// The parameter is the subset of testing.TB this needs — importing
// package testing from non-test code is wrong, and a caller outside a
// test has no business calling this.
//
// Not safe to call from parallel tests: the parameters are package
// globals. Keep at least one round-trip test on the production values so
// the real cost path stays exercised.
func SetFastHashParams(tb interface{ Cleanup(func()) }) {
	prevTime, prevMemory := argonTime, argonMemory
	argonTime, argonMemory = 1, 8*1024
	tb.Cleanup(func() {
		argonTime, argonMemory = prevTime, prevMemory
	})
}

// SaturateArgonQueue holds the argon2 slot and fills the waiter queue so the
// next password operation fails fast with ErrPasswordBusy. It is for tests of
// the busy path in other packages; the returned func (also registered with
// Cleanup) undoes it. Not safe to call from parallel tests.
func SaturateArgonQueue(tb interface {
	Cleanup(func())
	Fatalf(string, ...any)
}) func() {
	if err := argonSem.Acquire(context.Background(), argonConcurrency); err != nil {
		tb.Fatalf("saturate argon: %v", err)
	}
	argonWaiters.Add(maxArgonWaiters)
	var once sync.Once
	release := func() {
		once.Do(func() {
			argonWaiters.Add(-maxArgonWaiters)
			argonSem.Release(argonConcurrency)
		})
	}
	tb.Cleanup(release)
	return release
}
