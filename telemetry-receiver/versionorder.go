package main

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Kinds of a version change between two non-duplicate extended reports of
// one install (FR-042, research R22). Stored in daily_version_change.kind.
const (
	changeUpgraded   = "upgraded"
	changeDowngraded = "downgraded"
	changeUnordered  = "unordered"
)

// canonicalSemver returns label as a semver string with a leading "v", and
// whether it is valid semver. "0.3.0" and "v0.3.0" are the same version.
// Labels such as "pr617", "dev", "edge-<sha>", "other" and "invalid" are not
// semver (OD-8).
func canonicalSemver(label string) (string, bool) {
	v := label
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v, semver.IsValid(v)
}

// isSemver reports whether label is an ordered (semver) version.
func isSemver(label string) bool {
	_, ok := canonicalSemver(label)
	return ok
}

// classifyChange compares an install's previous version with its new one.
// changed is false when the labels are equal, or are the same semver version
// spelled differently. Otherwise kind is changeUpgraded or changeDowngraded
// when both sides are semver, and changeUnordered when either side is not.
func classifyChange(prev, next string) (kind string, changed bool) {
	if prev == next {
		return "", false
	}
	p, pok := canonicalSemver(prev)
	n, nok := canonicalSemver(next)
	if !pok || !nok {
		return changeUnordered, true
	}
	switch c := semver.Compare(n, p); {
	case c > 0:
		return changeUpgraded, true
	case c < 0:
		return changeDowngraded, true
	default:
		return "", false
	}
}
