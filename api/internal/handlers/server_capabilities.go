package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

type captureCapabilitiesProbe interface {
	Capabilities(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error)
}

type serverCapabilities struct {
	Target  fleetTarget         `json:"target"`
	Capture captureCapabilities `json:"capture"`
}

type captureCapabilities struct {
	Enabled                 bool   `json:"enabled"`
	Files                   bool   `json:"files"`
	State                   string `json:"state"`
	DefaultRetentionSeconds int64  `json:"defaultRetentionSeconds"`
	MaxRetentionSeconds     int64  `json:"maxRetentionSeconds"`
	DefaultMaxDurationSecs  int64  `json:"defaultMaxDurationSeconds"`
	DefaultMaxSizeBytes     int64  `json:"defaultMaxSizeBytes"`
}

// MountServerCapabilities describes supported transports, not permissions.
// The normal server-read RBAC middleware must authorize the exact target first.
func MountServerCapabilities(r chi.Router, reg *kube.Registry, probe captureCapabilitiesProbe, localConfig CaptureConfig) {
	r.Get("/servers/{name}/capabilities", func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		cluster, name := scope.RequestedCluster(req), chi.URLParam(req, "name")
		server, err := k.GetServer(req.Context(), ns, name)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if err := rbac.ValidateServerIdentity(req.Context(), cluster, ns, name, string(server.GetUID())); err != nil {
			httperr.Write(w, req, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name))
			return
		}
		out := serverCapabilities{
			Target:  fleetTarget{Cluster: cluster, Namespace: ns, Name: name, UID: string(server.GetUID())},
			Capture: captureCapabilities{State: "unavailable"},
		}
		if cluster == scope.DefaultCluster {
			out.Capture = captureCapabilities{
				Enabled: localConfig.FeatureEnabled, Files: true, State: "ready",
				DefaultRetentionSeconds: localConfig.DefaultRetentionSeconds,
				MaxRetentionSeconds:     localConfig.MaxRetentionSeconds,
				DefaultMaxDurationSecs:  int64(localConfig.DefaultMaxDurationSecs),
				DefaultMaxSizeBytes:     localConfig.DefaultMaxSizeBytes,
			}
		} else if probe != nil {
			caps, err := probe.Capabilities(req.Context(), cluster, ns, name)
			if err == nil {
				out.Capture.State = "unsupported"
				if caps.Protocol == "v1" && caps.TargetUID && caps.CaptureFiles {
					out.Capture = captureCapabilities{
						Enabled: caps.CaptureEnabled, Files: true, State: "ready",
						DefaultRetentionSeconds: caps.DefaultRetentionSeconds,
						MaxRetentionSeconds:     caps.MaxRetentionSeconds,
						DefaultMaxDurationSecs:  caps.DefaultMaxDurationSecs,
						DefaultMaxSizeBytes:     caps.DefaultMaxSizeBytes,
					}
				}
			}
		}
		writeJSON(w, out)
	})
}
