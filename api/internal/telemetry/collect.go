package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// extSchema is the extended-part schema version this build emits.
const extSchema = 1

// other is the value every enumerated field falls back to.
const other = "other"

// nonDigits matches a run of non-digits, which is stripped from the
// discovery minor version ("31+" on EKS and GKE becomes "31").
var nonDigits = regexp.MustCompile(`[^0-9]+`)

// Flags are the install settings Collect reads. They come from API flags
// and environment, which live in package main, so the caller copies the
// relevant values in.
type Flags struct {
	// CaptureEnabled mirrors --capture-enabled.
	CaptureEnabled bool
	// OIDCConfigured is true when the Helm OIDC issuer flag is set.
	OIDCConfigured bool
	// AuditWebhook is true when --audit-webhook-url is set.
	AuditWebhook bool
	// AuditS3 is true when both the S3 audit endpoint and bucket are set.
	AuditS3 bool
	// DBDriver is --db-driver, "sqlite" or "postgres".
	DBDriver string
	// OfficialModuleSource is --official-module-source; empty means every
	// module counts as custom.
	OfficialModuleSource string
}

// Deps is what Collect reads from. Kube and Store are required.
type Deps struct {
	Kube    *kube.Client
	Store   *db.Store
	Flags   Flags
	Version string
	// Extended asks for the extended part (the gate's extendedOn).
	Extended bool
	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time
}

// Collect builds the report the reporter would send now, and the same value
// backs the admin preview. The basic counts cover the local cluster only.
// When deps.Extended is set it also fills the extended part from the sources
// in research R14. A field whose source can't be read yields 0, false or
// "other" rather than an error; the only errors are a missing install ID and
// a failure to derive the signing key, because the part can't be signed
// without them.
func Collect(ctx context.Context, deps Deps) (telemetryschema.Report, error) {
	servers := listItems(ctx, deps, "servers")
	templates := listItems(ctx, deps, "templates")
	rep := telemetryschema.Report{
		Version:   deps.Version,
		Servers:   len(servers),
		Templates: len(templates),
	}
	if !deps.Extended {
		return rep, nil
	}
	ext, err := collectExtended(ctx, deps, servers, templates)
	if err != nil {
		return telemetryschema.Report{}, err
	}
	rep.Ext = ext
	return rep, nil
}

// listItems lists every object of one kube.GVRs kind across namespaces. A
// failure yields no items, so the count is 0.
func listItems(ctx context.Context, deps Deps, kind string) []unstructured.Unstructured {
	gvr, ok := kube.GVRs[kind]
	if !ok || deps.Kube == nil || deps.Kube.Dynamic == nil {
		return nil
	}
	list, err := deps.Kube.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return list.Items
}

// collectExtended builds the extended part and signs it with the key derived
// from the install ID and the stored secret.
func collectExtended(ctx context.Context, deps Deps, servers, templates []unstructured.Unstructured) (*telemetryschema.Extended, error) {
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	state, err := deps.Store.GetTelemetryState(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect telemetry: %w", err)
	}
	if state.InstallID == "" {
		return nil, errors.New("collect telemetry: extended report needs an install id")
	}
	secret, err := deps.Store.EnsureSigningSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect telemetry: %w", err)
	}
	priv, err := telemetryschema.DeriveKey(secret, state.InstallID)
	if err != nil {
		return nil, fmt.Errorf("collect telemetry: derive key: %w", err)
	}

	nodes, nodesOK := listNodes(ctx, deps)
	gitVersion, minor := serverVersion(deps)
	nodeBand := other
	if nodesOK && len(nodes) > 0 {
		nodeBand = telemetryschema.NodeBand(len(nodes))
	}

	return &telemetryschema.Extended{
		Schema:    extSchema,
		InstallID: state.InstallID,
		Env: telemetryschema.Env{
			K8s:    minor,
			Distro: detectDistro(gitVersion, nodes),
			Arch:   nodeArches(nodes),
			Nodes:  nodeBand,
		},
		Games:    countGames(ctx, deps, servers, templates),
		Features: collectFeatures(ctx, deps, servers),
		Key:      telemetryschema.PublicKeyString(priv),
		SentAt:   now().UTC().Truncate(time.Second),
	}, nil
}

// listNodes lists the local cluster's nodes. ok is false when the call failed.
func listNodes(ctx context.Context, deps Deps) ([]corev1.Node, bool) {
	if deps.Kube == nil || deps.Kube.Typed == nil {
		return nil, false
	}
	list, err := deps.Kube.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, false
	}
	return list.Items, true
}

// serverVersion returns the API server's gitVersion (for distro detection)
// and its minor version as "1.N", or "other" when it can't be read or doesn't
// fit the 1.N pattern. Non-digit suffixes such as the "+" of "31+" are
// dropped.
func serverVersion(deps Deps) (gitVersion, minor string) {
	if deps.Kube == nil || deps.Kube.Typed == nil {
		return "", other
	}
	v, err := deps.Kube.Typed.Discovery().ServerVersion()
	if err != nil || v == nil {
		return "", other
	}
	minor = v.Major + "." + nonDigits.ReplaceAllString(v.Minor, "")
	if !telemetryschema.K8sMinorRE.MatchString(minor) {
		minor = other
	}
	return v.GitVersion, minor
}

// nodeArches returns the sorted, unique CPU architectures of nodes. Values
// outside the enumeration become "other", and a cluster with no readable
// node reports ["other"] because the wire schema rejects an empty list.
func nodeArches(nodes []corev1.Node) []string {
	seen := map[string]bool{}
	for i := range nodes {
		a := nodes[i].Status.NodeInfo.Architecture
		if a == "" {
			continue
		}
		seen[telemetryschema.SanitizeEnum(telemetryschema.Arches, a)] = true
	}
	if len(seen) == 0 {
		return []string{other}
	}
	return sortedKeys(seen)
}

// sortedKeys returns the keys of set in ascending order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// countGames counts GameServers per official module and the rest as custom.
// A server whose template can't be found, or whose template isn't official
// (see officialModule), counts as custom. Only catalog names ever appear as
// keys.
func countGames(ctx context.Context, deps Deps, servers, templates []unstructured.Unstructured) telemetryschema.Games {
	labelsByTemplate := make(map[string]map[string]string, len(templates))
	for i := range templates {
		labelsByTemplate[templates[i].GetName()] = templates[i].GetLabels()
	}
	listed := map[string]bool{}
	if deps.Kube != nil {
		listed = listedModules(ctx, deps.Kube.Dynamic, deps.Flags.OfficialModuleSource)
	}
	games := telemetryschema.Games{Official: map[string]int{}}
	for i := range servers {
		tmplName, _, _ := unstructured.NestedString(servers[i].Object, "spec", "templateRef", "name")
		m := officialModule(labelsByTemplate[tmplName], deps.Flags.OfficialModuleSource, listed)
		if m == "" {
			games.Custom++
			continue
		}
		games.Official[m]++
	}
	return games
}

// collectFeatures reads the feature flags and bands from the local cluster
// and the API's own configuration.
func collectFeatures(ctx context.Context, deps Deps, servers []unstructured.Unstructured) telemetryschema.Features {
	tunnels := map[string]bool{}
	wake := false
	for i := range servers {
		if on, _, _ := unstructured.NestedBool(servers[i].Object, "spec", "idle", "wakeOnConnect"); on {
			wake = true
		}
		if on, _, _ := unstructured.NestedBool(servers[i].Object, "spec", "tunnel", "enabled"); on {
			p, _, _ := unstructured.NestedString(servers[i].Object, "spec", "tunnel", "provider")
			tunnels[telemetryschema.SanitizeEnum(telemetryschema.Tunnels, p)] = true
		}
	}
	return telemetryschema.Features{
		WakeOnConnect:   wake,
		Tunnels:         sortedKeys(tunnels),
		Capture:         deps.Flags.CaptureEnabled,
		Backups:         len(listItems(ctx, deps, "schedules")) > 0,
		SSO:             deps.Flags.OIDCConfigured || hasSSOProvider(ctx, deps.Store),
		AuditForwarding: deps.Flags.AuditWebhook || deps.Flags.AuditS3,
		Clusters:        telemetryschema.ClusterBand(localClusterCount(deps) + countClusters(ctx, deps)),
		DB:              telemetryschema.SanitizeEnum(telemetryschema.DBs, deps.Flags.DBDriver),
		Language:        "en",
	}
}

// countClusters counts registered Cluster CRs, 0 when they can't be listed.
func countClusters(ctx context.Context, deps Deps) int {
	if deps.Kube == nil || deps.Kube.Clusters() == nil {
		return 0
	}
	list, err := deps.Kube.Clusters().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0
	}
	return len(list.Items)
}

// hasSSOProvider reports whether the admin-managed "auth" config holds an
// enabled oidc, google or github provider. A missing, unreadable or
// malformed row is false.
func hasSSOProvider(ctx context.Context, store *db.Store) bool {
	if store == nil {
		return false
	}
	raw, ok, err := store.ConfigValue(ctx, "auth")
	if err != nil || !ok {
		return false
	}
	var cfg struct {
		Providers []struct {
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
		} `json:"providers"`
	}
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return false
	}
	for _, p := range cfg.Providers {
		if p.Enabled && (p.Kind == "oidc" || p.Kind == "google" || p.Kind == "github") {
			return true
		}
	}
	return false
}

func localClusterCount(deps Deps) int {
	if deps.Kube == nil || deps.Kube.IsStandalone() {
		return 0
	}
	return 1
}
