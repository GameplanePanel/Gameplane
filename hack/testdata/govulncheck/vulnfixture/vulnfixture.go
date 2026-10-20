// Package vulnfixture is a synthetic module used to prove the govulncheck gate
// (spec 013, G1). It is not published and is never fetched: the proof script
// vendors it at runtime.
package vulnfixture

// Bad returns a value from the vulnerable code path.
func Bad() string { return "bad" }

// Good returns a value from code that is not affected by the advisory.
func Good() string { return "good" }
