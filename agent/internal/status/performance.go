package status

import (
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/prometheus/client_golang/prometheus"
)

type performanceSample struct {
	value    float64
	observed time.Time
}

var decimalThreshold = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,6})?$`)

func validPerformance(p *caps.PerformanceMetric) bool {
	if p == nil {
		return false
	}
	if p.DegradedThreshold != nil && !decimalThreshold.MatchString(*p.DegradedThreshold) {
		return false
	}
	switch p.Signal {
	case "tick_rate", "frame_rate":
		return p.Unit == "per_second"
	case "tick_duration":
		return p.Unit == "seconds" || p.Unit == "milliseconds"
	default:
		return false
	}
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func baseValue(n float64, p *caps.PerformanceMetric) float64 {
	if p.Unit == "milliseconds" {
		return n / 1000
	}
	return n
}
func parsePerformance(raw string, p *caps.PerformanceMetric) (performanceSample, bool) {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finite(n) || n < 0 {
		return performanceSample{}, false
	}
	return performanceSample{value: baseValue(n, p)}, true
}

var performanceLabels = []string{"metric", "signal", "unit"}
var performanceValue = prometheus.NewDesc("gameplane_agent_performance_value", "Finite game performance measurement in the declared base unit; absent when unknown or stale.", performanceLabels, nil)
var performanceAvailable = prometheus.NewDesc("gameplane_agent_performance_available", "Whether the declared performance signal has a fresh valid sample.", performanceLabels, nil)
var performanceTimestamp = prometheus.NewDesc("gameplane_agent_performance_sample_timestamp_seconds", "Unix collection timestamp for a fresh valid game performance sample.", performanceLabels, nil)
var performanceThreshold = prometheus.NewDesc("gameplane_agent_performance_degraded_threshold", "Module-declared degradation threshold in the same base unit as the measurement.", append(append([]string{}, performanceLabels...), "direction"), nil)

// Describe publishes the fixed descriptors for explicitly declared performance signals.
func (s *Source) Describe(ch chan<- *prometheus.Desc) {
	ch <- performanceValue
	ch <- performanceAvailable
	ch <- performanceTimestamp
	ch <- performanceThreshold
}

// Collect exports cached performance samples and availability without issuing commands.
func (s *Source) Collect(ch chan<- prometheus.Metric) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.metrics {
		p := m.performance
		if p == nil {
			continue
		}
		unit, direction := "hertz", "below"
		if p.Signal == "tick_duration" {
			unit, direction = "seconds", "above"
		}
		labels := []string{m.id, p.Signal, unit}
		sample, ok := s.samples[m.id]
		fresh := !sample.observed.IsZero() && time.Since(sample.observed) < performanceFreshness
		available := 0.0
		if fresh && ok {
			available = 1
			ch <- prometheus.MustNewConstMetric(performanceValue, prometheus.GaugeValue, sample.value, labels...)
			ch <- prometheus.MustNewConstMetric(performanceTimestamp, prometheus.GaugeValue, float64(sample.observed.UnixNano())/1e9, labels...)
		}
		ch <- prometheus.MustNewConstMetric(performanceAvailable, prometheus.GaugeValue, available, labels...)
		if p.DegradedThreshold != nil {
			threshold, _ := strconv.ParseFloat(*p.DegradedThreshold, 64) // validated bounded decimal
			ch <- prometheus.MustNewConstMetric(performanceThreshold, prometheus.GaugeValue, baseValue(threshold, p), append(labels, direction)...)
		}
	}
}
