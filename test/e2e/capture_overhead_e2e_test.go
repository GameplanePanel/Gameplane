//go:build e2e

// Capture overhead regression guard (feature 007, US2).
//
// TestGameServer_CaptureOverhead_Joined boots one vanilla Minecraft Java server
// and runs sustained-hold probes in three phases: capture off, capture on (an
// ephemeral capture sidecar recording tcp port 25565), and capture disabled again
// (the sidecar stays present but idle, see OD-7). It validates the downloaded
// capture file, writes capture-overhead-metrics.json, and judges the result
// against the OD-8 thresholds.
//
// This is a regression guard, not SC-002 certification (FR-008): five probes per
// phase on a shared kind node cannot establish a latency benchmark. The CI job
// that runs this test is non-blocking and never blocks a merge (OD-1).
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/gameproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

const (
	// captureOverheadPort is the Minecraft game port the capture filter targets.
	captureOverheadPort = 25565
	// captureOverheadPhaseProbes is the number of sustained probes per phase.
	captureOverheadPhaseProbes = 5
	// captureOverheadHold is the sustained hold window per probe.
	captureOverheadHold = "30s"
	// captureOverheadPingEvery is the Ping Request interval during the hold.
	captureOverheadPingEvery = "2s"
	// captureOverheadProbeDeadline bounds one probe run, warm-up included.
	captureOverheadProbeDeadline = 3 * time.Minute
	// captureOverheadSettle is the wait after each state change before probing.
	captureOverheadSettle = 10 * time.Second
	// captureOverheadMaxDuration is the capture duration cap; it must outlast
	// the three phases (about 15 probes of roughly one minute each).
	captureOverheadMaxDuration = 1800
	// captureOverheadMinPasses is the fewest passes a capture-off phase needs
	// before the run counts as conclusive (OD-8).
	captureOverheadMinPasses = 4
	// captureOverheadArtifactFile is the metrics artifact name.
	captureOverheadArtifactFile = "capture-overhead-metrics.json"
	// captureOverheadStepTimeout bounds each capture state transition.
	captureOverheadStepTimeout = 60 * time.Second
	// captureOverheadReadyTimeout bounds the sidecar becoming ready.
	captureOverheadReadyTimeout = 90 * time.Second
)

// captureOverheadProbe is one sustained probe attempt in a phase.
type captureOverheadProbe struct {
	Attempt int    `json:"attempt"`
	Pass    bool   `json:"pass"`
	Failure string `json:"failure"`
	// Metrics is the raw METRICS json from the probe, null when it printed none.
	Metrics json.RawMessage `json:"metrics"`
}

// captureOverheadSummary holds RTT and handshake statistics for one phase.
type captureOverheadSummary struct {
	RTTAvgMs              *float64 `json:"rtt_avg_ms"`
	RTTMinMs              *float64 `json:"rtt_min_ms"`
	RTTMaxMs              *float64 `json:"rtt_max_ms"`
	HandshakeLatencyAvgMs *float64 `json:"handshake_latency_avg_ms"`
}

// captureOverheadPhase is one of the three measurement phases.
type captureOverheadPhase struct {
	Phase          string                 `json:"phase"`
	CaptureEnabled bool                   `json:"capture_enabled"`
	SidecarPresent bool                   `json:"sidecar_present"`
	PassCount      int                    `json:"pass_count"`
	Probes         []captureOverheadProbe `json:"probes"`
	Summary        captureOverheadSummary `json:"summary"`
}

// captureOverheadCapture is the validation result for the downloaded capture.
type captureOverheadCapture struct {
	Packets          int    `json:"packets"`
	NonFilterPackets int    `json:"non_filter_packets"`
	Streams          int    `json:"streams"`
	ClassifyFailures int    `json:"classify_failures"`
	Valid            bool   `json:"valid"`
	Error            string `json:"error"`
}

// captureOverheadVerdict is the OD-8 judgement over the whole run.
type captureOverheadVerdict struct {
	TotalPasses       int      `json:"total_passes"`
	SustainedTimeouts int      `json:"sustained_timeouts"`
	Verdict           string   `json:"verdict"`
	Reasons           []string `json:"reasons"`
}

// captureOverheadReport is the capture-overhead-metrics.json document.
type captureOverheadReport struct {
	Phases            []captureOverheadPhase `json:"phases"`
	Capture           captureOverheadCapture `json:"capture"`
	ServerTickRate    *float64               `json:"server_tick_rate_ticks_per_sec"`
	TickRateNote      string                 `json:"tick_rate_note"`
	RestartCountDelta int32                  `json:"restart_count_delta"`
	Summary           captureOverheadVerdict `json:"summary"`
}

// captureOverheadMetrics is the subset of the probe's METRICS object this test reads.
type captureOverheadMetrics struct {
	HandshakeLatencyMs    float64   `json:"handshake_latency_ms"`
	KeepaliveRTTSamplesMs []float64 `json:"keepalive_rtt_samples_ms"`
	Failure               string    `json:"failure"`
}

// TestGameServer_CaptureOverhead_Joined is the capture overhead regression guard.
// It is not parallel: one Minecraft server, one capture and one admin login.
func TestGameServer_CaptureOverhead_Joined(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")

	artifactDir := captureOverheadArtifactDir(t)
	rep := &captureOverheadReport{
		Phases:       []captureOverheadPhase{},
		TickRateNote: "tick rate unavailable (check server type in template)",
	}

	// The artifact is written before judging, and on any early exit too.
	finalized := false
	finalize := func() {
		if finalized {
			return
		}
		finalized = true
		rep.Summary = judgeCaptureOverhead(rep)
		writeCaptureOverheadArtifact(t, artifactDir, rep)
	}
	defer finalize()

	spec := minecraftBotSpec()
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	gsName, ns := createGameBotServer(t, envInstance, spec)

	restartsBefore, podUIDBefore := captureOverheadGameContainer(t, ns, gsName)

	// Phase 1: capture off, no sidecar.
	rep.Phases = append(rep.Phases, runCaptureOverheadPhase(t, ns, gsName, spec.Game, "capture-off-1", false))

	// Phase 2: enable the sidecar, start a capture, then probe.
	var captureErrs []string
	recordErr := func(stage string, err error) {
		t.Logf("warning: capture %s failed: %v", stage, err)
		captureErrs = append(captureErrs, fmt.Sprintf("%s: %v", stage, err))
	}
	enabled := false
	captureID := ""
	if err := captureOverheadPost(cli, "/servers/"+gsName+":capture-enable", nil, http.StatusOK); err != nil {
		recordErr("enable", err)
	} else {
		enabled = true
		if err := awaitCaptureOverhead(captureOverheadReadyTimeout, func() (bool, string, error) {
			obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
				Get(context.Background(), gsName, metav1.GetOptions{})
			if err != nil {
				return false, "", fmt.Errorf("get gameserver: %w", err)
			}
			ready, _, _ := unstructured.NestedBool(obj.Object, "status", "capture", "ready")
			return ready, fmt.Sprintf("status.capture.ready=%t", ready), nil
		}); err != nil {
			recordErr("sidecar ready", err)
		} else {
			id, err := captureOverheadStart(t, cli, ns, gsName)
			captureID = id
			if err != nil {
				recordErr("start", err)
			}
		}
	}
	time.Sleep(captureOverheadSettle)

	rep.Phases = append(rep.Phases, runCaptureOverheadPhase(t, ns, gsName, spec.Game, "capture-on", true))

	// Stop, download and validate whatever capture was started.
	if captureID != "" {
		data, err := captureOverheadStopAndDownload(cli, ns, gsName, captureID)
		if err != nil {
			recordErr("stop/download", err)
		} else {
			rep.Capture = analyzeCaptureOverheadFile(data)
			t.Logf("capture file size=%d bytes, packets=%d, non-filter=%d, streams=%d, classify-failures=%d (warnings)",
				len(data), rep.Capture.Packets, rep.Capture.NonFilterPackets, rep.Capture.Streams, rep.Capture.ClassifyFailures)
		}
	}
	if len(captureErrs) > 0 {
		rep.Capture.Error = strings.Join(captureErrs, "; ")
	}

	// Phase 3 runs after disable. The sidecar stays present but idle (OD-7).
	if enabled {
		if err := captureOverheadPost(cli, "/servers/"+gsName+":capture-disable", nil, http.StatusOK); err != nil {
			recordErr("disable", err)
			rep.Capture.Error = strings.Join(captureErrs, "; ")
		}
	}
	time.Sleep(captureOverheadSettle)

	rep.Phases = append(rep.Phases, runCaptureOverheadPhase(t, ns, gsName, spec.Game, "capture-off-2", false))

	restartsAfter, podUIDAfter := captureOverheadGameContainer(t, ns, gsName)
	rep.RestartCountDelta = restartsAfter - restartsBefore
	if podUIDAfter != podUIDBefore {
		t.Logf("warning: pod %s-0 was replaced during the run (UID %s -> %s); restart delta may not be meaningful",
			gsName, podUIDBefore, podUIDAfter)
	}

	finalize()
	logCaptureOverheadRTTDeltas(t, rep)
	switch rep.Summary.Verdict {
	case "regression":
		t.Errorf("capture overhead regression (OD-8): %s", strings.Join(rep.Summary.Reasons, "; "))
	case "inconclusive":
		t.Logf("⚠ INCONCLUSIVE capture overhead (OD-8): %s", strings.Join(rep.Summary.Reasons, "; "))
	default:
		t.Logf("capture overhead ok (OD-8): total passes %d across 3 phases", rep.Summary.TotalPasses)
	}
}

// captureOverheadArtifactDir returns $GAMEPLANE_E2E_ARTIFACT_DIR, or a temp dir
// when it is unset. The path is logged by the writer.
func captureOverheadArtifactDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("GAMEPLANE_E2E_ARTIFACT_DIR"); dir != "" {
		return dir
	}
	return t.TempDir()
}

// writeCaptureOverheadArtifact writes the report as indented JSON and logs it.
func writeCaptureOverheadArtifact(t *testing.T, dir string, rep *captureOverheadReport) {
	t.Helper()
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Errorf("marshal %s: %v", captureOverheadArtifactFile, err)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Errorf("create artifact dir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, captureOverheadArtifactFile)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Errorf("write %s: %v", path, err)
		return
	}
	t.Logf("%s written to %s", captureOverheadArtifactFile, path)
	t.Logf("%s:\n%s", captureOverheadArtifactFile, data)
}

// captureOverheadPost posts to an API route and requires the wanted status.
func captureOverheadPost(cli *APIClient, path string, body any, want int) error {
	resp, respBody, err := cli.Post(path, body)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("POST %s: status %s, body %s", path, resp.Status, respBody)
	}
	return nil
}

// captureOverheadStart starts a capture covering the game port and waits for the
// NetworkCapture to reach Running. It returns the captureId even when the wait
// fails, so the caller can still stop the capture.
func captureOverheadStart(t *testing.T, cli *APIClient, ns, gsName string) (string, error) {
	req := map[string]any{
		"filter":                  fmt.Sprintf("tcp port %d", captureOverheadPort),
		"maxDurationSeconds":      captureOverheadMaxDuration,
		"maxSizeBytes":            testCaptureMaxSize,
		"ttlSecondsAfterFinished": 3600,
	}
	body, err := postCaptureOverheadBody(cli, "/servers/"+gsName+":capture-start", req, http.StatusAccepted)
	if err != nil {
		return "", err
	}
	var started struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		return "", fmt.Errorf("parse capture-start response %q: %w", body, err)
	}
	captureID := started.CaptureID
	if captureID == "" {
		return "", fmt.Errorf("capture-start response had no captureId: %s", body)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Delete(context.Background(), captureID, metav1.DeleteOptions{})
	})
	err = awaitCaptureOverhead(captureOverheadStepTimeout, func() (bool, string, error) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(context.Background(), captureID, metav1.GetOptions{})
		if err != nil {
			return false, "", fmt.Errorf("get networkcapture: %w", err)
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		return phase == "Running", "phase=" + phase, nil
	})
	if err != nil {
		return captureID, fmt.Errorf("networkcapture %s did not reach Running: %w", captureID, err)
	}
	return captureID, nil
}

// captureOverheadStopAndDownload stops the capture, waits for Completed and
// returns the capture file bytes.
func captureOverheadStopAndDownload(cli *APIClient, ns, gsName, captureID string) ([]byte, error) {
	if err := captureOverheadPost(cli, "/servers/"+gsName+":capture-stop",
		map[string]any{"captureId": captureID}, http.StatusOK); err != nil {
		return nil, err
	}
	if err := awaitCaptureOverhead(captureOverheadStepTimeout, func() (bool, string, error) {
		obj, err := envInstance.Dyn.Resource(networkCaptureGVR).Namespace(ns).
			Get(context.Background(), captureID, metav1.GetOptions{})
		if err != nil {
			return false, "", fmt.Errorf("get networkcapture: %w", err)
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		return phase == "Completed", "phase=" + phase, nil
	}); err != nil {
		return nil, fmt.Errorf("networkcapture %s did not reach Completed: %w", captureID, err)
	}

	resp, body, err := cli.Get("/servers/" + gsName + ":capture-file?id=" + captureID)
	if err != nil {
		return nil, fmt.Errorf("download capture file: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download capture file: status %s", resp.Status)
	}
	if len(body) == 0 {
		return nil, errors.New("capture file is empty")
	}
	return body, nil
}

// postCaptureOverheadBody is captureOverheadPost that also returns the response body.
func postCaptureOverheadBody(cli *APIClient, path string, body any, want int) ([]byte, error) {
	resp, respBody, err := cli.Post(path, body)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != want {
		return respBody, fmt.Errorf("POST %s: status %s, body %s", path, resp.Status, respBody)
	}
	return respBody, nil
}

// analyzeCaptureOverheadFile validates a PCAPNG capture (OD-6). Every TCP packet
// must carry the game port; packets outside the filter are counted and make the
// capture invalid. Client-to-server payloads are reassembled per 4-tuple in
// capture order and classified with gameproto. Classification failures are
// counted as warnings only.
func analyzeCaptureOverheadFile(data []byte) captureOverheadCapture {
	var out captureOverheadCapture
	reader, err := pcapgo.NewNgReader(bytes.NewReader(data), pcapgo.DefaultNgReaderOptions)
	if err != nil {
		out.Error = fmt.Sprintf("not valid PCAPNG: %v", err)
		return out
	}

	streams := map[string]*bytes.Buffer{}
	var order []string
	for {
		pkt, _, err := reader.ReadPacketData()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			out.Error = fmt.Sprintf("read packet %d: %v", out.Packets+1, err)
			break
		}
		out.Packets++

		packet := gopacket.NewPacket(pkt, reader.LinkType(), gopacket.Default)
		netLayer := packet.NetworkLayer()
		tcp, ok := packet.Layer(layers.LayerTypeTCP).(*layers.TCP)
		if !ok || netLayer == nil || (tcp.SrcPort != captureOverheadPort && tcp.DstPort != captureOverheadPort) {
			out.NonFilterPackets++
			continue
		}
		if tcp.DstPort != captureOverheadPort || len(tcp.Payload) == 0 {
			continue
		}
		flow := netLayer.NetworkFlow()
		key := fmt.Sprintf("%s:%d>%s:%d", flow.Src(), tcp.SrcPort, flow.Dst(), tcp.DstPort)
		buf, seen := streams[key]
		if !seen {
			buf = &bytes.Buffer{}
			streams[key] = buf
			order = append(order, key)
		}
		buf.Write(tcp.Payload)
	}

	out.Streams = len(order)
	for _, key := range order {
		res, err := (&gameproto.MinecraftClassifier{}).Classify(bufio.NewReader(bytes.NewReader(streams[key].Bytes())))
		if err != nil || res == nil || res.Kind == gameproto.Unknown {
			out.ClassifyFailures++
		}
	}

	out.Valid = out.Error == "" && out.Packets > 0 && out.NonFilterPackets == 0
	return out
}

// runCaptureOverheadPhase runs captureOverheadPhaseProbes sustained probes. A
// failed probe is recorded and the phase continues.
func runCaptureOverheadPhase(t *testing.T, ns, gsName, game, phase string, captureOn bool) captureOverheadPhase {
	t.Helper()
	ph := captureOverheadPhase{
		Phase:          phase,
		CaptureEnabled: captureOn,
		SidecarPresent: captureOverheadSidecarPresent(t, ns, gsName),
		Probes:         []captureOverheadProbe{},
	}
	for attempt := 1; attempt <= captureOverheadPhaseProbes; attempt++ {
		res := envInstance.RunGameProbe(t, GameProbe{
			GameNS:      ns,
			GSName:      gsName,
			Game:        game,
			Port:        captureOverheadPort,
			Deadline:    captureOverheadProbeDeadline,
			ExpectDepth: joindepth.JOINED,
			Args: []string{
				"-mode", "sustained",
				"-hold", captureOverheadHold,
				"-ping-every", captureOverheadPingEvery,
			},
			JobSuffix: fmt.Sprintf("%s-%d", phase, attempt),
		})
		m := parseCaptureOverheadMetrics(res.Metrics)
		probe := captureOverheadProbe{Attempt: attempt, Metrics: res.Metrics}
		probe.Pass = res.ExitCode == 0 && m.Failure == ""
		if !probe.Pass {
			probe.Failure = captureOverheadFailure(res, m)
		}
		if probe.Pass {
			ph.PassCount++
		}
		ph.Probes = append(ph.Probes, probe)
		t.Logf("%s probe %d: pass=%t failure=%q", phase, attempt, probe.Pass, probe.Failure)
	}
	ph.Summary = summarizeCaptureOverheadPhase(ph.Probes)
	t.Logf("%s: %d/%d passes", phase, ph.PassCount, captureOverheadPhaseProbes)
	return ph
}

// captureOverheadFailure returns the probe's failure class, falling back to the
// verdict when the METRICS line did not say why.
func captureOverheadFailure(res *ProbeResult, m captureOverheadMetrics) string {
	if m.Failure != "" {
		return m.Failure
	}
	if res.Verdict != nil {
		return fmt.Sprintf("exit %d: %s", res.ExitCode, res.Verdict.String())
	}
	return fmt.Sprintf("exit %d: no verdict", res.ExitCode)
}

// parseCaptureOverheadMetrics decodes the METRICS object. A nil or undecodable
// payload yields the zero value.
func parseCaptureOverheadMetrics(raw json.RawMessage) captureOverheadMetrics {
	var m captureOverheadMetrics
	if len(raw) == 0 {
		return m
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return captureOverheadMetrics{}
	}
	return m
}

// summarizeCaptureOverheadPhase aggregates RTT and handshake latency across the
// phase's probes that printed metrics.
func summarizeCaptureOverheadPhase(probes []captureOverheadProbe) captureOverheadSummary {
	var rtts, handshakes []float64
	for _, p := range probes {
		m := parseCaptureOverheadMetrics(p.Metrics)
		if len(p.Metrics) == 0 {
			continue
		}
		rtts = append(rtts, m.KeepaliveRTTSamplesMs...)
		handshakes = append(handshakes, m.HandshakeLatencyMs)
	}
	avg, lo, hi := captureOverheadStats(rtts)
	hAvg, _, _ := captureOverheadStats(handshakes)
	return captureOverheadSummary{
		RTTAvgMs:              avg,
		RTTMinMs:              lo,
		RTTMaxMs:              hi,
		HandshakeLatencyAvgMs: hAvg,
	}
}

// captureOverheadStats returns avg, min and max, or nils when vals is empty.
func captureOverheadStats(vals []float64) (avg, lo, hi *float64) {
	if len(vals) == 0 {
		return nil, nil, nil
	}
	sum := 0.0
	minV, maxV := vals[0], vals[0]
	for _, v := range vals {
		sum += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	a := sum / float64(len(vals))
	return &a, &minV, &maxV
}

// judgeCaptureOverhead applies OD-8. Regression: capture-on passes under half of
// the lower capture-off pass count, the game container restarted, or the capture
// is invalid. Inconclusive: a capture-off phase under captureOverheadMinPasses,
// or a probe failure short of the regression bar. RTT deltas are not judged.
func judgeCaptureOverhead(rep *captureOverheadReport) captureOverheadVerdict {
	v := captureOverheadVerdict{Reasons: []string{}}
	anyFailed := false
	for _, ph := range rep.Phases {
		v.TotalPasses += ph.PassCount
		for _, p := range ph.Probes {
			if !p.Pass {
				anyFailed = true
			}
			if p.Failure == "timeout" {
				v.SustainedTimeouts++
			}
		}
	}

	if len(rep.Phases) != 3 {
		v.Verdict = "inconclusive"
		v.Reasons = append(v.Reasons, fmt.Sprintf("run incomplete: %d of 3 phases recorded", len(rep.Phases)))
		return v
	}

	off1, on, off2 := rep.Phases[0].PassCount, rep.Phases[1].PassCount, rep.Phases[2].PassCount
	minOff := off1
	if off2 < minOff {
		minOff = off2
	}

	regression := false
	if on*2 < minOff {
		regression = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("capture-on passes %d/%d are more than 50%% below the lower capture-off pass count %d/%d",
			on, captureOverheadPhaseProbes, minOff, captureOverheadPhaseProbes))
	}
	if rep.RestartCountDelta > 0 {
		regression = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("game container restart count increased by %d", rep.RestartCountDelta))
	}
	if !rep.Capture.Valid || rep.Capture.Error != "" {
		regression = true
		v.Reasons = append(v.Reasons, "capture invalid: "+captureOverheadInvalidReason(rep.Capture))
	}

	switch {
	case regression:
		v.Verdict = "regression"
	case off1 < captureOverheadMinPasses || off2 < captureOverheadMinPasses || anyFailed:
		v.Verdict = "inconclusive"
		if off1 < captureOverheadMinPasses {
			v.Reasons = append(v.Reasons, fmt.Sprintf("capture-off-1 passes %d/%d below %d", off1, captureOverheadPhaseProbes, captureOverheadMinPasses))
		}
		if off2 < captureOverheadMinPasses {
			v.Reasons = append(v.Reasons, fmt.Sprintf("capture-off-2 passes %d/%d below %d", off2, captureOverheadPhaseProbes, captureOverheadMinPasses))
		}
		if anyFailed {
			v.Reasons = append(v.Reasons, "one or more probes failed (possible environmental transience, OD-5)")
		}
	default:
		v.Verdict = "ok"
	}
	return v
}

// captureOverheadInvalidReason names why a capture failed validation.
func captureOverheadInvalidReason(c captureOverheadCapture) string {
	switch {
	case c.Error != "":
		return c.Error
	case c.Packets == 0:
		return "capture contains no packets"
	default:
		return fmt.Sprintf("%d packets outside filter tcp port %d", c.NonFilterPackets, captureOverheadPort)
	}
}

// logCaptureOverheadRTTDeltas logs capture-on versus capture-off RTT averages.
// Deltas are informational only (OD-8).
func logCaptureOverheadRTTDeltas(t *testing.T, rep *captureOverheadReport) {
	t.Helper()
	if len(rep.Phases) != 3 {
		return
	}
	off1, on, off2 := rep.Phases[0].Summary.RTTAvgMs, rep.Phases[1].Summary.RTTAvgMs, rep.Phases[2].Summary.RTTAvgMs
	if off1 == nil || on == nil || off2 == nil {
		t.Logf("RTT deltas unavailable: a phase recorded no Ping/Pong samples")
		return
	}
	base := (*off1 + *off2) / 2
	if base == 0 {
		return
	}
	t.Logf("RTT avg ms: off-1=%.2f on=%.2f off-2=%.2f; on vs off mean delta %+.1f%% (informational, not judged)",
		*off1, *on, *off2, (*on-base)/base*100)
}

// captureOverheadSidecarPresent reports whether the capture ephemeral container
// is on the pod spec.
func captureOverheadSidecarPresent(t *testing.T, ns, gsName string) bool {
	t.Helper()
	pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod %s-0: %v", gsName, err)
	}
	for _, ec := range pod.Spec.EphemeralContainers {
		if ec.Name == "capture" {
			return true
		}
	}
	return false
}

// captureOverheadGameContainer returns the game container restart count and the
// pod UID for the GameServer's pod.
func captureOverheadGameContainer(t *testing.T, ns, gsName string) (int32, string) {
	t.Helper()
	pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), gsName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod %s-0: %v", gsName, err)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "game" {
			return cs.RestartCount, string(pod.UID)
		}
	}
	t.Fatalf("no game container status in pod %s-0", gsName)
	return 0, ""
}

// awaitCaptureOverhead polls cond every 2s until it reports done, returns an
// error, or timeout expires. The state string is included in a timeout error.
func awaitCaptureOverhead(timeout time.Duration, cond func() (bool, string, error)) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		done, state, err := cond()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		last = state
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s (last state: %s)", timeout, last)
		}
		time.Sleep(2 * time.Second)
	}
}
