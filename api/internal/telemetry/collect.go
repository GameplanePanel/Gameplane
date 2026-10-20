package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// extSchema is the extended-part schema version this build emits.
const extSchema = 2

// other is the value every enumerated field falls back to.
const other = "other"

// clusterTimeout bounds the calls the report makes to one managed cluster, so
// a cluster that doesn't answer can't hold the report past its slot (FR-044).
const clusterTimeout = 10 * time.Second

// Deployment markers (FR-045).
const (
	deploymentCluster    = "cluster"
	deploymentStandalone = "standalone"
)

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
	Kube *kube.Client
	// Registry holds every cluster the panel manages: the local cluster, when
	// there is one, and each registered remote cluster. Nil means the local
	// cluster (Kube) only.
	Registry *kube.Registry
	Store    *db.Store
	Flags    Flags
	Version  string
	// Extended asks for the extended part (the gate's extendedOn).
	Extended bool
	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time
}

// clusterData is what one reachable managed cluster contributes to a report.
// The fields after templates are read only for an extended report.
type clusterData struct {
	servers    []unstructured.Unstructured
	templates  []unstructured.Unstructured
	nodes      []corev1.Node
	nodesOK    bool
	gitVersion string
	minor      string
	games      telemetryschema.Games
	backups    bool
}

// Collect builds the report the reporter would send now, and the same value
// backs the admin preview. The counts cover every managed cluster that can
// be listed: the local cluster, when there is one, and every registered
// remote cluster (FR-043); a cluster that can't be listed is left out
// (FR-044). When deps.Extended is set it also fills the extended part from
// the sources in research R14 and R23. A field whose source can't be read
// yields 0, false or "other" rather than an error; the only errors are a
// missing install ID and a failure to derive the signing key, because the
// part can't be signed without them.
func Collect(ctx context.Context, deps Deps) (telemetryschema.Report, error) {
	clusters := readClusters(ctx, deps)
	rep := telemetryschema.Report{Version: deps.Version}
	for _, c := range clusters {
		rep.Servers += len(c.servers)
		rep.Templates += len(c.templates)
	}
	if !deps.Extended {
		return rep, nil
	}
	ext, err := collectExtended(ctx, deps, clusters)
	if err != nil {
		return telemetryschema.Report{}, err
	}
	rep.Ext = ext
	return rep, nil
}

// managedClusters returns the clients of the clusters the report covers:
// every cluster in deps.Registry, or the local cluster when there is no
// registry. A standalone panel's management client has no Kubernetes
// transport and is never one of them.
func managedClusters(deps Deps) []*kube.Client {
	if deps.Registry == nil {
		if deps.Kube == nil || deps.Kube.IsStandalone() {
			return nil
		}
		return []*kube.Client{deps.Kube}
	}
	var out []*kube.Client
	for _, id := range deps.Registry.IDs() {
		if c, ok := deps.Registry.Get(id); ok && c != nil && !c.IsStandalone() {
			out = append(out, c)
		}
	}
	return out
}

// readClusters reads every managed cluster at once, each under its own
// clusterTimeout, so slow clusters can't add up (FR-044). It lists GameServers
// and GameTemplates and, for an extended report, nodes, the server version,
// game counts and backup schedules. A cluster whose GameServer, GameTemplate
// or (for an extended report) backup schedule list fails is left out of the
// report (R23), so its servers are never counted with partial data. The result
// keeps the registry's order.
func readClusters(ctx context.Context, deps Deps) []clusterData {
	clients := managedClusters(deps)
	results := make([]*clusterData, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cd := deps
			cd.Kube = c
			cctx, cancel := context.WithTimeout(ctx, clusterTimeout)
			defer cancel()
			servers, err := listKind(cctx, cd, "servers")
			if err != nil {
				return
			}
			templates, err := listKind(cctx, cd, "templates")
			if err != nil {
				return
			}
			r := &clusterData{servers: servers, templates: templates}
			if deps.Extended {
				r.nodes, r.nodesOK = listNodes(cctx, cd)
				r.gitVersion, r.minor = serverVersionCtx(cctx, cd)
				r.games = countGames(cctx, cd, r.servers, r.templates)
				schedules, err := listKind(cctx, cd, "schedules")
				if err != nil {
					return
				}
				r.backups = len(schedules) > 0
			}
			results[i] = r
		}()
	}
	wg.Wait()
	out := make([]clusterData, 0, len(results))
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// listKind lists every object of one kube.GVRs kind across namespaces on
// deps.Kube. It fails when the kind is unknown, there is no dynamic client,
// or the list call fails.
func listKind(ctx context.Context, deps Deps, kind string) ([]unstructured.Unstructured, error) {
	gvr, ok := kube.GVRs[kind]
	if !ok {
		return nil, fmt.Errorf("list %s: unknown kind", kind)
	}
	if deps.Kube == nil || deps.Kube.Dynamic == nil {
		return nil, fmt.Errorf("list %s: no cluster client", kind)
	}
	list, err := deps.Kube.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, err)
	}
	return list.Items, nil
}

// listItems is listKind with a failure read as no items, so the count is 0.
func listItems(ctx context.Context, deps Deps, kind string) []unstructured.Unstructured {
	items, err := listKind(ctx, deps, kind)
	if err != nil {
		return nil
	}
	return items
}

// collectExtended builds the extended part over the reachable clusters and
// signs it with the key derived from the install ID and the stored secret.
func collectExtended(ctx context.Context, deps Deps, clusters []clusterData) (*telemetryschema.Extended, error) {
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

	// Environment over every reachable cluster (OD-15, OD-16): k8s and
	// distro are sets, arch is the union, and the node band counts all nodes.
	k8s, distros := map[string]bool{}, map[string]bool{}
	var allNodes []corev1.Node
	var servers []unstructured.Unstructured
	nodesOK, backups := false, false
	games := telemetryschema.Games{Official: map[string]int{}}
	for _, c := range clusters {
		if c.nodesOK {
			nodesOK = true
			allNodes = append(allNodes, c.nodes...)
		}
		backups = backups || c.backups
		k8s[c.minor] = true
		distros[detectDistro(c.gitVersion, c.nodes)] = true
		for m, n := range c.games.Official {
			games.Official[m] += n
		}
		games.Custom += c.games.Custom
		servers = append(servers, c.servers...)
	}
	nodeBand := other
	if nodesOK && len(allNodes) > 0 {
		nodeBand = telemetryschema.NodeBand(len(allNodes))
	}
	features := collectFeatures(ctx, deps, servers)
	features.Backups = backups

	return &telemetryschema.Extended{
		Schema:    extSchema,
		InstallID: state.InstallID,
		Env: telemetryschema.Env{
			K8s:        setOrOther(k8s),
			Distro:     setOrOther(distros),
			Arch:       nodeArches(allNodes),
			Nodes:      nodeBand,
			Deployment: deployment(deps),
		},
		Games:    games,
		Features: features,
		Key:      telemetryschema.PublicKeyString(priv),
		SentAt:   now().UTC().Truncate(time.Second),
	}, nil
}

// deployment is the FR-045 marker: standalone when the panel runs without a
// local cluster, cluster otherwise.
func deployment(deps Deps) string {
	if deps.Kube.IsStandalone() {
		return deploymentStandalone
	}
	return deploymentCluster
}

// setOrOther returns the sorted members of set, or ["other"] when it is
// empty, because the wire schema rejects an empty list.
func setOrOther(set map[string]bool) []string {
	if len(set) == 0 {
		return []string{other}
	}
	return sortedKeys(set)
}

// listNodes lists the nodes of deps.Kube. ok is false when the call failed.
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

// serverVersionCtx is serverVersion bounded by ctx: the discovery call takes
// no context, so it runs aside and an expired ctx reads as "other". The call
// itself is bounded by discoveryClient's timeout.
func serverVersionCtx(ctx context.Context, deps Deps) (gitVersion, minor string) {
	type result struct{ git, minor string }
	ch := make(chan result, 1)
	go func() {
		g, m := serverVersion(deps)
		ch <- result{g, m}
	}()
	select {
	case r := <-ch:
		return r.git, r.minor
	case <-ctx.Done():
		return "", other
	}
}

// serverVersion returns the API server's gitVersion (for distro detection)
// and its minor version as "1.N", or "other" when it can't be read or doesn't
// fit the 1.N pattern. Non-digit suffixes such as the "+" of "31+" are
// dropped.
func serverVersion(deps Deps) (gitVersion, minor string) {
	if deps.Kube == nil || deps.Kube.Typed == nil {
		return "", other
	}
	v, err := discoveryClient(deps.Kube).ServerVersion()
	if err != nil || v == nil {
		return "", other
	}
	minor = v.Major + "." + nonDigits.ReplaceAllString(v.Minor, "")
	if !telemetryschema.K8sMinorRE.MatchString(minor) {
		minor = other
	}
	return v.GitVersion, minor
}

// discoveryClient returns a discovery client for c whose requests time out
// after clusterTimeout, so a hung cluster can't hold a discovery call open
// after the report gave up on it. Clients built without a rest.Config (test
// fakes) use their own discovery client.
func discoveryClient(c *kube.Client) discovery.DiscoveryInterface {
	if c.Config == nil {
		return c.Typed.Discovery()
	}
	cfg := rest.CopyConfig(c.Config)
	cfg.Timeout = clusterTimeout
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return c.Typed.Discovery()
	}
	return d
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

// collectFeatures reads the feature flags and bands from servers, the
// GameServers of every reachable cluster, and the API's own configuration.
// Backups is left false; collectExtended sets it over every reachable
// cluster.
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
