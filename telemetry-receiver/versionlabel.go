package main

import "github.com/GameplanePanel/gameplane/telemetryschema"

// Version label budget (spec 022 FR-014 label safety). At most
// maxVersionLabels valid versions hold their own label at a time. Once every
// slot is taken, a new version waits in a bounded pending set, counted under
// "other", until it has been reported more often than the least reported
// retained label; it then takes that label's slot. This keeps the label set
// bounded while junk versions sent once or twice can no longer lock real
// releases out of the dashboard until the next restart.
const (
	// maxPendingVersions bounds the versions waiting for a slot.
	maxPendingVersions = 1024
	labelInvalid       = "invalid"
	labelOther         = "other"
)

// versionBook is the label budget. The caller holds server.versionMu.
type versionBook struct {
	// retained maps each labelled version to the reports counted under it.
	retained map[string]int
	// pending maps a version waiting for a slot to its reports so far.
	pending map[string]int
}

func newVersionBook() versionBook {
	return versionBook{retained: make(map[string]int), pending: make(map[string]int)}
}

// versionLabel returns the label a report of version is stored and counted
// under, and counts the report toward that version's standing. A version
// that wins a slot evicts the least reported retained label, whose
// Prometheus series is deleted so the series count stays bounded.
func (s *server) versionLabel(version string) string {
	if !telemetryschema.VersionRE.MatchString(version) || version == labelInvalid {
		return labelInvalid
	}
	if version == labelOther {
		return labelOther
	}
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	b := &s.versions
	if n, ok := b.retained[version]; ok {
		b.retained[version] = n + 1
		return version
	}
	if len(b.retained) < maxVersionLabels {
		b.retained[version] = 1
		return version
	}
	n := b.pending[version] + 1
	if _, ok := b.pending[version]; !ok && len(b.pending) >= maxPendingVersions {
		dropLeast(b.pending)
	}
	b.pending[version] = n
	victim, least := leastReported(b.retained)
	if n <= least {
		return labelOther
	}
	delete(b.retained, victim)
	s.reports.DeleteLabelValues(victim)
	delete(b.pending, version)
	b.retained[version] = n
	return version
}

// countReport increments the reports counter for label, a value
// versionLabel returned. A label evicted in the meantime is counted under
// "other", so a late increment never recreates a deleted series.
func (s *server) countReport(label string) {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if label != labelInvalid && label != labelOther {
		if _, ok := s.versions.retained[label]; !ok {
			label = labelOther
		}
	}
	s.reports.WithLabelValues(label).Inc()
}

// leastReported returns the key of m with the smallest count (ties by
// key, so the choice is deterministic) and that count.
func leastReported(m map[string]int) (string, int) {
	key, least := "", -1
	for k, n := range m {
		if least < 0 || n < least || (n == least && k < key) {
			key, least = k, n
		}
	}
	return key, least
}

// dropLeast removes the entry of m with the smallest count.
func dropLeast(m map[string]int) {
	if k, n := leastReported(m); n >= 0 {
		delete(m, k)
	}
}
