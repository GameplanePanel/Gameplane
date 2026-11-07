// Package nodecapture runs one bounded capture against an operator-selected
// containerd game process. It never accepts a host PID or filesystem path.
package nodecapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/capture-sidecar/internal/capture"
	"golang.org/x/sys/unix"
)

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)
var podPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var containerPattern = regexp.MustCompile(`^containerd://[0-9a-f]{64}$`)
var capturePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// The separate artifact server reads finalized files through the dedicated PVC group.
const artifactMode os.FileMode = 0o640

// Config contains immutable identities copied from live Kubernetes objects.
type Config struct {
	ServerUID       string `json:"serverUID"`
	CaptureUID      string `json:"captureUID"`
	CaptureID       string `json:"captureID"`
	PodUID          string `json:"podUID"`
	ContainerID     string `json:"containerID"`
	Node            string `json:"node"`
	Filter          string `json:"filter"`
	DurationSeconds int64  `json:"durationSeconds"`
	MaxSizeBytes    int64  `json:"maxSizeBytes"`
}

// Validate rejects unsupported runtimes, unconstrained captures and paths.
func (c Config) Validate() error {
	if !identityPattern.MatchString(c.ServerUID) || !identityPattern.MatchString(c.CaptureUID) || !capturePattern.MatchString(c.CaptureID) || !podPattern.MatchString(c.PodUID) || !containerPattern.MatchString(c.ContainerID) || c.Node == "" || c.Filter == "" || len(c.Filter) > 1024 || c.DurationSeconds < 1 || c.DurationSeconds > 3600 || c.MaxSizeBytes < 1 || c.MaxSizeBytes > 900*1024*1024 {
		return errors.New("invalid node capture identity, runtime, filter or limits")
	}
	return nil
}

// Result is written atomically only after the PCAP has been closed.
type Result struct {
	ServerUID      string `json:"serverUID"`
	CaptureUID     string `json:"captureUID"`
	CaptureID      string `json:"captureId"`
	Status         string `json:"status"`
	PacketsWritten int64  `json:"packetsWritten"`
	BytesWritten   int64  `json:"bytesWritten"`
	Message        string `json:"message"`
}

func matchesCgroup(body, pod, id string) bool {
	for _, line := range strings.Split(body, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		p := parts[2]
		podMatch := strings.Contains(p, "/pod"+pod+"/") || strings.Contains(p, "-pod"+strings.ReplaceAll(pod, "-", "_")+".slice/")
		containerMatch := strings.HasSuffix(p, "/"+id) || strings.HasSuffix(p, "/cri-containerd-"+id+".scope")
		if podMatch && containerMatch {
			return true
		}
	}
	return false
}

type targetProcess struct {
	root      *os.Root
	namespace *os.File
	pod, id   string
}

func (t *targetProcess) close() { _ = t.namespace.Close(); _ = t.root.Close() }
func (t *targetProcess) verify() error {
	body, err := t.root.ReadFile("cgroup")
	if err != nil || !matchesCgroup(string(body), t.pod, t.id) {
		return errors.New("target container identity changed")
	}
	return nil
}

func findTarget(c Config) (*targetProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read host processes: %w", err)
	}
	id := strings.TrimPrefix(c.ContainerID, "containerd://")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		root, err := os.OpenRoot(filepath.Join("/proc", entry.Name()))
		if err != nil {
			continue
		}
		body, err := root.ReadFile("cgroup")
		if err != nil || !matchesCgroup(string(body), c.PodUID, id) {
			_ = root.Close()
			continue
		}
		// /proc namespace descriptors are magic symlinks, so os.Root correctly
		// refuses Open on them. Open through this already-verified numeric PID,
		// then verify the pinned proc directory again before and after setns.
		ns, err := os.Open(filepath.Join("/proc", entry.Name(), "ns/net"))
		if err != nil {
			_ = root.Close()
			return nil, fmt.Errorf("open target network namespace: %w", err)
		}
		t := &targetProcess{root: root, namespace: ns, pod: c.PodUID, id: id}
		if err := t.verify(); err != nil {
			t.close()
			return nil, err
		}
		return t, nil
	}
	return nil, errors.New("containerd target process not found on selected node")
}

func (t *targetProcess) source(filter string) (capture.PacketSource, error) {
	type opened struct {
		s   capture.PacketSource
		err error
	}
	done := make(chan opened, 1)
	go func() {
		// Never return a thread in the target netns to Go's thread pool.
		// A goroutine exiting with its OS thread locked terminates that thread;
		// the AF_PACKET descriptor remains bound to the original target netns.
		runtime.LockOSThread()
		if err := t.verify(); err != nil {
			done <- opened{err: err}
			return
		}
		if err := unix.Setns(int(t.namespace.Fd()), unix.CLONE_NEWNET); err != nil {
			done <- opened{err: fmt.Errorf("enter target network namespace: %w", err)}
			return
		}
		if err := t.verify(); err != nil {
			done <- opened{err: err}
			return
		}
		s, err := capture.NewAFPacketSource("eth0", 0, filter)
		done <- opened{s: s, err: err}
	}()
	r := <-done
	return r.s, r.err
}

// Run captures once and exits. SIGTERM cancels ctx and finalizes the file;
// controller completion waits for worker Pods to disappear before publishing.
func Run(ctx context.Context, c Config, dir string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	target, err := findTarget(c)
	if err != nil {
		return err
	}
	defer target.close()
	source, err := target.source(c.Filter)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	return runCapture(ctx, c, dir, source, target.verify)
}

func runCapture(ctx context.Context, c Config, dir string, source capture.PacketSource, verify func() error) error {
	identity, err := json.Marshal(map[string]string{"serverUID": c.ServerUID, "captureUID": c.CaptureUID})
	if err != nil {
		return err
	}
	output, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open capture output directory: %w", err)
	}
	defer func() { _ = output.Close() }()
	identityName := "capture-" + c.CaptureID + ".identity.json"
	f, err := output.OpenFile(identityName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reserve capture identity: %w", err)
	}
	if _, err = f.Write(identity); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// fsGroup grants the nonroot artifact server access after the worker exits.
	if err = output.Chmod(identityName, artifactMode); err != nil {
		return err
	}
	writer, err := capture.NewWriter(filepath.Join(dir, "capture-"+c.CaptureID+".pcapng"), c.DurationSeconds, c.MaxSizeBytes, 0, c.Filter)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.DurationSeconds)*time.Second)
	defer cancel()
	identityChanged := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if verify() != nil {
					identityChanged <- struct{}{}
					cancel()
					return
				}
			}
		}
	}()
	result := Result{ServerUID: c.ServerUID, CaptureUID: c.CaptureUID, CaptureID: c.CaptureID, Status: "completed"}
	for {
		packet, err := source.ReadPacket(ctx)
		if err != nil {
			if ctx.Err() == nil {
				result.Status = "failed"
				result.Message = "packet source failed"
			}
			break
		}
		if err := writer.WritePacket(packet); err != nil {
			if !errors.Is(err, capture.ErrLimitReached) {
				result.Status = "failed"
				result.Message = "capture write failed"
			}
			break
		}
	}
	select {
	case <-identityChanged:
		result.Status = "failed"
		result.Message = "target container identity changed"
	default:
	}
	if verify() != nil {
		result.Status = "failed"
		result.Message = "target container identity changed"
	}
	if err := writer.Close(); err != nil {
		result.Status = "failed"
		result.Message = "capture finalization failed"
	}
	result.PacketsWritten = writer.PacketsWritten()
	result.BytesWritten = writer.BytesWritten()
	if err := output.Chmod("capture-"+c.CaptureID+".pcapng", artifactMode); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	path := "capture-" + c.CaptureID + ".result.json"
	if err := output.WriteFile(path+".tmp", raw, artifactMode); err != nil {
		return err
	}
	return output.Rename(path+".tmp", path)
}
