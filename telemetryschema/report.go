package telemetryschema

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"
)

// Report is one telemetry report: the basic tier (Version, Servers,
// Templates) plus the optional extended tier (Ext). Ext is non-nil only while
// the install's extended tier is on.
type Report struct {
	Version   string    `json:"version"`
	Servers   int       `json:"servers"`
	Templates int       `json:"templates"`
	Ext       *Extended `json:"ext,omitempty"`
}

// Extended is the extended tier of a report. Every field is required on the
// wire. Key is the base64url (unpadded) Ed25519 public key derived for
// InstallID (see DeriveKey); SentAt is the send time.
type Extended struct {
	Schema    int       `json:"schema"`
	InstallID string    `json:"installId"`
	Env       Env       `json:"env"`
	Games     Games     `json:"games"`
	Features  Features  `json:"features"`
	Key       string    `json:"key"`
	SentAt    time.Time `json:"sentAt"`
}

// Env describes the install's environment, using only enumerated values.
// From schema 2 it covers every reachable managed cluster: K8s and Distro are
// sets like Arch, and Deployment says whether the panel runs on a cluster or
// standalone. A schema 1 report decodes K8s and Distro as one-value sets and
// Deployment as "other".
type Env struct {
	K8s        []string `json:"k8s"`
	Distro     []string `json:"distro"`
	Arch       []string `json:"arch"`
	Nodes      string   `json:"nodes"`
	Deployment string   `json:"deployment"`
}

// Games counts local GameServers: Official maps an official catalog module
// name to its server count, Custom counts every other server.
type Games struct {
	Official map[string]int `json:"official"`
	Custom   int            `json:"custom"`
}

// Features records which optional features are in use.
type Features struct {
	WakeOnConnect   bool     `json:"wakeOnConnect"`
	Tunnels         []string `json:"tunnels"`
	Capture         bool     `json:"capture"`
	Backups         bool     `json:"backups"`
	SSO             bool     `json:"sso"`
	AuditForwarding bool     `json:"auditForwarding"`
	Clusters        string   `json:"clusters"`
	DB              string   `json:"db"`
	Language        string   `json:"language"`
}

// wireExtended is Extended as it appears on the wire: SentAt is a
// second-precision RFC 3339 UTC string. Env is a wireEnv1 or a wireEnv2, by Schema.
type wireExtended struct {
	Schema    int      `json:"schema"`
	InstallID string   `json:"installId"`
	Env       any      `json:"env"`
	Games     Games    `json:"games"`
	Features  Features `json:"features"`
	Key       string   `json:"key"`
	SentAt    string   `json:"sentAt"`
}

// wireEnv1 is Env as schema 1 writes it: k8s and distro are single values and
// there is no deployment.
type wireEnv1 struct {
	K8s    string   `json:"k8s"`
	Distro string   `json:"distro"`
	Arch   []string `json:"arch"`
	Nodes  string   `json:"nodes"`
}

// wireEnv2 is Env as schema 2 writes it (OD-13, OD-15).
type wireEnv2 struct {
	K8s        []string `json:"k8s"`
	Distro     []string `json:"distro"`
	Arch       []string `json:"arch"`
	Nodes      string   `json:"nodes"`
	Deployment string   `json:"deployment"`
}

// wireReport is Report as it appears on the wire.
type wireReport struct {
	Version   string        `json:"version"`
	Servers   int           `json:"servers"`
	Templates int           `json:"templates"`
	Ext       *wireExtended `json:"ext,omitempty"`
}

// Encode returns the canonical JSON body for r. The output is deterministic:
// arrays are sorted and de-duplicated, object keys are sorted (encoding/json
// sorts map keys), nil arrays and maps are written as [] and {} (never null),
// and SentAt is written in UTC at second precision. Schema 1 writes env.k8s and env.distro as single values and no deployment; later schemas write them as sets with the deployment. Encode does not fold
// out-of-set values; Decode does. Negative counts are rejected with
// ErrInvalidPayload.
func Encode(r Report) ([]byte, error) {
	if r.Servers < 0 || r.Templates < 0 {
		return nil, fmt.Errorf("%w: counts must be non-negative", ErrInvalidPayload)
	}
	w := wireReport{Version: r.Version, Servers: r.Servers, Templates: r.Templates}
	if e := r.Ext; e != nil {
		official := make(map[string]int, len(e.Games.Official))
		for k, v := range e.Games.Official {
			official[k] = v
		}
		var env any = wireEnv2{
			K8s: sortedSet(e.Env.K8s), Distro: sortedSet(e.Env.Distro), Arch: sortedSet(e.Env.Arch),
			Nodes: e.Env.Nodes, Deployment: e.Env.Deployment,
		}
		if e.Schema < 2 {
			env = wireEnv1{K8s: single(e.Env.K8s), Distro: single(e.Env.Distro), Arch: sortedSet(e.Env.Arch), Nodes: e.Env.Nodes}
		}
		feat := e.Features
		feat.Tunnels = sortedSet(feat.Tunnels)
		w.Ext = &wireExtended{
			Schema:    e.Schema,
			InstallID: e.InstallID,
			Env:       env,
			Games:     Games{Official: official, Custom: e.Games.Custom},
			Features:  feat,
			Key:       e.Key,
			SentAt:    e.SentAt.UTC().Format(time.RFC3339),
		}
	}
	out, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("telemetryschema: encode report: %w", err)
	}
	return out, nil
}

// sortedSet returns a sorted, de-duplicated copy of in. It never returns nil,
// so the JSON form is [] rather than null.
func sortedSet(in []string) []string {
	out := slices.Clone(in)
	if out == nil {
		out = []string{}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// single returns the one value of a set for a schema 1 field: "" for an
// empty set, the value for a one-value set, and "other" for more than one.
func single(set []string) string {
	switch s := sortedSet(set); len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	default:
		return other
	}
}
