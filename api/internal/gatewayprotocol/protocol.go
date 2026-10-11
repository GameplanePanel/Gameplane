// Package gatewayprotocol defines the private, versioned agent gateway protocol.
package gatewayprotocol

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Target binds an operation to one immutable GameServer identity.
type Target struct {
	Cluster   string
	Namespace string
	Name      string
	UID       string
}

var uidPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

// Path constructs a gateway path from validated identity components.
func Path(target Target, agentPath string) (string, error) {
	if len(validation.IsDNS1123Subdomain(target.Cluster)) != 0 ||
		len(validation.IsDNS1123Label(target.Namespace)) != 0 ||
		len(validation.IsDNS1123Label(target.Name)) != 0 || !uidPattern.MatchString(target.UID) {
		return "", errors.New("invalid gateway target")
	}
	if !knownPath(agentPath) {
		return "", errors.New("unsupported agent path")
	}
	return "/v1/clusters/" + target.Cluster + "/namespaces/" + target.Namespace + "/servers/" + target.Name + "/uids/" + target.UID + agentPath, nil
}

// ParsePath accepts only the canonical versioned gateway route.
func ParsePath(path string) (Target, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 11 || parts[0] != "" || parts[1] != "v1" || parts[2] != "clusters" || parts[4] != "namespaces" || parts[6] != "servers" || parts[8] != "uids" {
		return Target{}, "", errors.New("invalid gateway path")
	}
	target := Target{Cluster: parts[3], Namespace: parts[5], Name: parts[7], UID: parts[9]}
	agentPath := "/" + strings.Join(parts[10:], "/")
	canonical, err := Path(target, agentPath)
	if err != nil || canonical != path {
		return Target{}, "", errors.New("invalid gateway path")
	}
	return target, agentPath, nil
}

// Allowed returns the request body cap for the exact supported operation.
func Allowed(method, path string) (int64, bool) {
	switch method {
	case http.MethodGet:
		switch path {
		case "/console", "/logs/tail", "/logs/download", "/files/list", "/files/read", "/files/download", "/players", "/players/banned", "/players/whitelist", "/status", "/mods":
			return 0, true
		}
	case http.MethodPost:
		switch path {
		case "/actions/run":
			return 16 << 10, true
		case "/mods/upload":
			return 512 << 20, true
		case "/files/write", "/files/create", "/files/upload", "/files/mkdir", "/players/kick", "/players/ban", "/players/unban", "/players/whitelist/add", "/players/whitelist/remove", "/mods/install":
			return 64 << 20, true
		}
	case http.MethodDelete:
		if path == "/files/delete" || path == "/mods" {
			return 64 << 20, true
		}
	}
	return 0, false
}

// Streaming identifies routes whose response is a WebSocket stream.
func Streaming(path string) bool { return path == "/console" || path == "/logs/tail" }

func knownPath(path string) bool {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if _, ok := Allowed(method, path); ok {
			return true
		}
	}
	return false
}
