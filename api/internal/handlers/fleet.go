package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const (
	fleetMaxItems     = 2000
	fleetPageSize     = 200
	fleetMaxScopes    = 128
	fleetMaxScan      = 10000
	fleetWorkers      = 4
	fleetTimeout      = 10 * time.Second
	fleetScopeTimeout = 5 * time.Second
)

// MountFleet exposes authenticated, permission-filtered reads across clusters.
// Mutations continue through the existing explicitly targeted resource routes.
func MountFleet(r chi.Router, reg *kube.Registry, store *db.Store) {
	h := fleetHandler{reg: reg, store: store}
	limit := auth.NewFleetReadLimiter().UserMiddleware
	for _, kind := range []string{"servers", "backups", "schedules", "restores"} {
		r.With(limit).Get("/fleet/"+kind, h.resources(kind))
	}
	r.With(limit).Get("/fleet/inventory", h.inventory)
	r.With(limit).Get("/fleet/placements", h.placements)
	r.Get("/servers/{name}/access", h.serverAccess)
}

type fleetHandler struct {
	reg   *kube.Registry
	store *db.Store
}

type fleetTarget struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

type fleetServerAccess struct {
	CanWrite       bool `json:"canWrite"`
	CanControl     bool `json:"canControl"`
	CanConsole     bool `json:"canConsole"`
	CanDelete      bool `json:"canDelete"`
	IsOwner        bool `json:"isOwner"`
	IsCollaborator bool `json:"isCollaborator"`
}

type fleetResource struct {
	Target      fleetTarget               `json:"target"`
	Resource    unstructured.Unstructured `json:"resource"`
	Permissions []string                  `json:"permissions"`
	Access      *fleetServerAccess        `json:"access,omitempty"`
}

type fleetInventory struct {
	Cluster string       `json:"cluster"`
	Name    string       `json:"name"`
	View    clusterView  `json:"view"`
	Stats   clusterStats `json:"stats"`
}

type fleetPlacement struct {
	Cluster   string                      `json:"cluster"`
	Namespace string                      `json:"namespace"`
	Templates []unstructured.Unstructured `json:"templates"`
}

type fleetIssue struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type fleetResult[T any] struct {
	Items         []T              `json:"items"`
	Partial       bool             `json:"partial"`
	Issues        []fleetIssue     `json:"issues"`
	TotalReturned int              `json:"totalReturned"`
	Scopes        []fleetScopeView `json:"scopes,omitempty"`
}

type fleetScopeView struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

func finishFleetScopes[T any](out *fleetResult[T]) {
	seen := make(map[fleetScopeView]bool, len(out.Scopes))
	unique := make([]fleetScopeView, 0, len(out.Scopes))
	for _, item := range out.Scopes {
		if !seen[item] {
			seen[item] = true
			unique = append(unique, item)
		}
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].Cluster != unique[j].Cluster {
			return unique[i].Cluster < unique[j].Cluster
		}
		return unique[i].Namespace < unique[j].Namespace
	})
	if len(unique) > fleetMaxItems {
		unique = unique[:fleetMaxItems]
		out.Partial = true
		out.Issues = append(out.Issues, *fleetSafeIssue("", "", "limit"))
	}
	out.Scopes = unique
}

type fleetFilter struct {
	cluster   string
	namespace string
	limit     int
}

func parseFleetFilter(req *http.Request) (fleetFilter, error) {
	q := req.URL.Query()
	f := fleetFilter{cluster: strings.TrimSpace(q.Get("cluster")), namespace: strings.TrimSpace(q.Get("namespace")), limit: fleetMaxItems}
	for _, key := range []string{"cluster", "namespace", "limit"} {
		if len(q[key]) > 1 {
			return f, errors.New("filters must have one value")
		}
	}
	if f.cluster != "" && !dnsLabelRE.MatchString(f.cluster) {
		return f, errors.New("cluster must be an exact cluster ID")
	}
	if f.namespace != "" && !scope.Allowed(f.namespace) {
		return f, scope.ErrForbiddenNamespace
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > fleetMaxItems {
			return f, errors.New("limit must be between 1 and 2000")
		}
		f.limit = n
	}
	return f, nil
}

type fleetCluster struct {
	id   string
	name string
	k    *kube.Client
}

// candidates includes persisted registrations even when their clients failed
// to load. Never expose this unfiltered list or raw discovery errors to callers.
func (h fleetHandler) candidates(ctx context.Context, filter string) ([]fleetCluster, []fleetIssue) {
	byID := map[string]fleetCluster{}
	for _, id := range h.reg.IDs() {
		k, _ := h.reg.Get(id)
		byID[id] = fleetCluster{id: id, name: id, k: k}
	}
	if _, ok := byID[h.reg.DefaultID()]; !ok && h.reg.Default() != nil {
		byID[h.reg.DefaultID()] = fleetCluster{id: h.reg.DefaultID(), name: h.reg.DefaultID()}
	}
	issues := make([]fleetIssue, 0)
	home := h.reg.Management()
	applyRegistration := func(obj *unstructured.Unstructured) {
		id := obj.GetName()
		if obj.GetDeletionTimestamp() != nil {
			delete(byID, id)
			return
		}
		if id == h.reg.DefaultID() {
			return
		}
		k, _ := h.reg.Get(id)
		name, _, _ := unstructured.NestedString(obj.Object, "spec", "displayName")
		if name == "" {
			name = id
		}
		byID[id] = fleetCluster{id: id, name: name, k: k}
	}
	// The local registration is intrinsic; its explicit filter does not
	// depend on discovering unrelated remote registrations.
	if filter != h.reg.DefaultID() && (home == nil || home.Clusters() == nil) {
		issues = append(issues, fleetIssue{Code: "unavailable", Message: "Cluster discovery is incomplete"})
	} else if filter != h.reg.DefaultID() {
		ctx, cancel := context.WithTimeout(ctx, fleetScopeTimeout)
		defer cancel()
		var list *unstructured.UnstructuredList
		var err error
		if filter != "" {
			var obj *unstructured.Unstructured
			obj, err = home.Clusters().Get(ctx, filter, metav1.GetOptions{})
			if err == nil {
				list = &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*obj}}
			}
		} else {
			list, err = home.Clusters().List(ctx, metav1.ListOptions{Limit: fleetMaxItems})
		}
		if err != nil && !apierrors.IsNotFound(err) {
			issues = append(issues, fleetIssue{Code: "unavailable", Message: "Cluster discovery is incomplete"})
		} else if err == nil {
			if list.GetContinue() != "" || len(list.Items) > fleetMaxItems {
				issues = append(issues, fleetIssue{Code: "limit", Message: "Cluster discovery reached its limit; narrow the filter"})
			}
			for i := range min(len(list.Items), fleetMaxItems) {
				applyRegistration(&list.Items[i])
			}
		}
	}
	out := make([]fleetCluster, 0, len(byID))
	for id, c := range byID {
		if filter == "" || id == filter {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, issues
}

func fleetReadPermission(kind string) string {
	if kind == "restores" {
		return "backups:read"
	}
	return kind + ":read"
}

func serverFleetAccess(u *auth.User, cl, ns string, obj *unstructured.Unstructured) fleetServerAccess {
	owner := isServerOwner(obj, u.ID)
	collaborator := false
	if !owner {
		for _, raw := range strings.Split(obj.GetAnnotations()[collaboratorsAnnotation], ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err == nil && id == u.ID {
				collaborator = true
				break
			}
		}
	}
	write := u.Can("servers:write", true, cl, ns)
	return fleetServerAccess{
		CanWrite: write, CanControl: write || owner || collaborator,
		CanConsole: u.Can("servers:console", true, cl, ns) || owner || collaborator,
		CanDelete:  u.Can("*", true, cl, ns) || owner,
		IsOwner:    owner, IsCollaborator: collaborator,
	}
}

type fleetScope struct {
	cluster   fleetCluster
	namespace string
	granted   bool
}

type fleetScopeResult struct {
	items   []fleetResource
	issue   *fleetIssue
	partial bool
}

func (h fleetHandler) resources(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		u, f, ok := h.start(w, req)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), fleetTimeout)
		defer cancel()
		clusters, discoveryIssues := h.candidates(ctx, f.cluster)
		out := fleetResult[fleetResource]{Items: make([]fleetResource, 0), Issues: discoveryIssues, Partial: len(discoveryIssues) > 0}
		namespaces := append([]string(nil), scope.AllowedNamespaces...)
		sort.Strings(namespaces)
		namespaces = compactStrings(namespaces)
		scopes := make([]fleetScope, 0)
		for _, c := range clusters {
			for _, ns := range namespaces {
				if f.namespace != "" && ns != f.namespace {
					continue
				}
				granted := u.Can(fleetReadPermission(kind), true, c.id, ns)
				if granted {
					out.Scopes = append(out.Scopes, fleetScopeView{Cluster: c.id, Namespace: ns})
				}
				// Ownership is a per-server read fallback, never a backup,
				// schedule or restore grant. Scan only configured namespaces.
				if kind == "servers" || granted {
					scopes = append(scopes, fleetScope{cluster: c, namespace: ns, granted: granted})
				}
			}
		}
		// Spend bounded work on explicit grants before ownership-only scans.
		sort.SliceStable(scopes, func(i, j int) bool { return scopes[i].granted && !scopes[j].granted })
		if len(scopes) > fleetMaxScopes {
			scopes = scopes[:fleetMaxScopes]
			out.Partial = true
			out.Issues = append(out.Issues, fleetIssue{Code: "limit", Message: "Scope limit reached; narrow the cluster or namespace filter"})
		}
		results := make([]fleetScopeResult, len(scopes))
		var scanBudget atomic.Int64
		scanBudget.Store(fleetMaxScan)
		fleetParallel(len(scopes), func(i int) {
			results[i] = h.readScope(ctx, u, kind, scopes[i], &scanBudget)
		})
		for _, result := range results {
			out.Items = append(out.Items, result.items...)
			for _, item := range result.items {
				out.Scopes = append(out.Scopes, fleetScopeView{Cluster: item.Target.Cluster, Namespace: item.Target.Namespace})
			}
			out.Partial = out.Partial || result.partial
			if result.issue != nil {
				out.Issues = append(out.Issues, *result.issue)
			}
		}
		// A guessed exact ID must not reveal a registration through the
		// partial bit alone. Until a grant or an owned object establishes
		// authority, an empty filtered result is the same as an unknown ID.
		if f.cluster != "" && len(out.Items) == 0 && len(out.Scopes) == 0 {
			out.Partial = false
			out.Issues = make([]fleetIssue, 0)
		}
		finishFleetScopes(&out)
		sort.Slice(out.Items, func(i, j int) bool {
			a, b := out.Items[i].Target, out.Items[j].Target
			if a.Cluster != b.Cluster {
				return a.Cluster < b.Cluster
			}
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			return a.Name < b.Name
		})
		if len(out.Items) > f.limit {
			out.Items = out.Items[:f.limit]
			out.Partial = true
			out.Issues = append(out.Issues, fleetIssue{Code: "limit", Message: "Result limit reached; narrow the cluster or namespace filter"})
		}
		out.TotalReturned = len(out.Items)
		writeJSON(w, out)
	}
}

func (h fleetHandler) start(w http.ResponseWriter, req *http.Request) (*auth.User, fleetFilter, bool) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return nil, fleetFilter{}, false
	}
	f, err := parseFleetFilter(req)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return nil, f, false
	}
	if h.reg == nil {
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("cluster registry unavailable"))
		return nil, f, false
	}
	return u, f, true
}

func (h fleetHandler) readScope(ctx context.Context, u *auth.User, kind string, s fleetScope, budget *atomic.Int64) fleetScopeResult {
	out := fleetScopeResult{}
	fail := func(code string) fleetScopeResult {
		out.partial = true
		// An owner-only scan must not disclose unrelated registrations.
		if s.granted || len(out.items) > 0 {
			out.issue = fleetSafeIssue(s.cluster.id, s.namespace, code)
		}
		return out
	}
	if s.cluster.k == nil || s.cluster.k.Dynamic == nil {
		return fail("unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, fleetScopeTimeout)
	defer cancel()
	cfgRules := newConfigRuleCache(s.cluster.k)
	continuation := ""
	seen := map[string]bool{}
	for scanned := 0; ; {
		if ctx.Err() != nil {
			return fail("unavailable")
		}
		if scanned >= fleetMaxItems || !reserveFleetPage(budget) {
			return fail("limit")
		}
		list, err := s.cluster.k.Dynamic.Resource(kube.GVRs[kind]).Namespace(s.namespace).
			List(ctx, metav1.ListOptions{Limit: fleetPageSize, Continue: continuation})
		if err != nil {
			return fail(fleetErrorCode(err))
		}
		if len(list.Items) < fleetPageSize {
			budget.Add(int64(fleetPageSize - len(list.Items)))
		}
		for i := range min(len(list.Items), fleetPageSize) {
			if scanned >= fleetMaxItems {
				return fail("limit")
			}
			scanned++
			obj := list.Items[i]
			// Never trust a malformed upstream list to cross namespace bounds.
			if obj.GetNamespace() != s.namespace {
				continue
			}
			if seen[obj.GetName()] {
				continue
			}
			seen[obj.GetName()] = true
			var access *fleetServerAccess
			if kind == "servers" {
				permissions := serverFleetAccess(u, s.cluster.id, s.namespace, &obj)
				if !s.granted && !permissions.IsOwner && !permissions.IsCollaborator {
					continue
				}
				access = &permissions
				gateStaleAgent(&obj)
				cfgRules.redact(ctx, &obj)
			}
			out.items = append(out.items, fleetResource{Target: fleetTarget{
				Cluster: s.cluster.id, Namespace: s.namespace, Name: obj.GetName(), UID: string(obj.GetUID()),
			}, Resource: obj, Permissions: fleetPermissions(u, s.cluster.id, s.namespace), Access: access})
		}
		if len(list.Items) > fleetPageSize {
			return fail("limit")
		}
		if list.GetContinue() == "" {
			return out
		}
		if list.GetContinue() == continuation {
			return fail("limit")
		}
		continuation = list.GetContinue()
	}
}

func reserveFleetPage(budget *atomic.Int64) bool {
	for {
		remaining := budget.Load()
		if remaining < fleetPageSize {
			return false
		}
		if budget.CompareAndSwap(remaining, remaining-fleetPageSize) {
			return true
		}
	}
}

func fleetErrorCode(err error) string {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return "forbidden"
	}
	return "unavailable"
}

func fleetSafeIssue(cluster, namespace, code string) *fleetIssue {
	message := "Inventory is unavailable for this scope"
	if code == "forbidden" {
		message = "The cluster connection cannot read this inventory"
	}
	if code == "limit" {
		message = "Inventory limit reached; narrow the filter"
	}
	return &fleetIssue{Cluster: cluster, Namespace: namespace, Code: code, Message: message}
}

func fleetParallel(n int, work func(int)) {
	jobs := make(chan int, n)
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for i := 0; i < min(n, fleetWorkers); i++ {
		workers.Go(func() {
			for job := range jobs {
				work(job)
			}
		})
	}
	workers.Wait()
}

func compactStrings(items []string) []string {
	out := items[:0]
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}

// inventory requires exact cluster-wide inventory grants. Server ownership or
// namespace roles never authorize nodes, volumes, or cluster health metadata.
func (h fleetHandler) inventory(w http.ResponseWriter, req *http.Request) {
	u, f, ok := h.start(w, req)
	if !ok {
		return
	}
	if f.namespace != "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("inventory has no namespace filter"))
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), fleetTimeout)
	defer cancel()
	clusters, issues := h.candidates(ctx, f.cluster)
	allowed := make([]fleetCluster, 0)
	for _, c := range clusters {
		if u.Can("cluster:read", true, c.id, "") {
			allowed = append(allowed, c)
		}
	}
	out := fleetResult[fleetInventory]{Items: make([]fleetInventory, 0), Issues: issues, Partial: len(issues) > 0}
	for _, c := range allowed {
		out.Scopes = append(out.Scopes, fleetScopeView{Cluster: c.id})
	}
	finishFleetScopes(&out)
	if len(allowed) > min(f.limit, fleetMaxScopes) {
		allowed = allowed[:min(f.limit, fleetMaxScopes)]
		out.Partial = true
		out.Issues = append(out.Issues, fleetIssue{Code: "limit", Message: "Inventory scope limit reached; narrow the cluster filter"})
	}
	type result struct {
		item  *fleetInventory
		issue *fleetIssue
	}
	results := make([]result, len(allowed))
	fleetParallel(len(allowed), func(i int) { results[i].item, results[i].issue = h.readInventory(ctx, allowed[i]) })
	for _, r := range results {
		if r.item != nil {
			out.Items = append(out.Items, *r.item)
		}
		if r.issue != nil {
			out.Partial = true
			out.Issues = append(out.Issues, *r.issue)
		}
	}
	out.TotalReturned = len(out.Items)
	writeJSON(w, out)
}

func (h fleetHandler) readInventory(ctx context.Context, c fleetCluster) (*fleetInventory, *fleetIssue) {
	if c.k == nil || c.k.Typed == nil {
		return nil, fleetSafeIssue(c.id, "", "unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, fleetScopeTimeout)
	defer cancel()
	nodes, err := c.k.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: fleetMaxItems})
	if err != nil {
		return nil, fleetSafeIssue(c.id, "", fleetErrorCode(err))
	}
	if nodes.GetContinue() != "" || len(nodes.Items) > fleetMaxItems {
		return nil, fleetSafeIssue(c.id, "", "limit")
	}
	pvs, err := c.k.Typed.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{Limit: fleetMaxItems})
	if err != nil {
		return nil, fleetSafeIssue(c.id, "", fleetErrorCode(err))
	}
	if pvs.GetContinue() != "" || len(pvs.Items) > fleetMaxItems {
		return nil, fleetSafeIssue(c.id, "", "limit")
	}
	selected := &clusterHandler{reg: h.reg, k: c.k, clusterID: c.id, store: h.store}
	// Optional metrics get their own short budget. Missing usage stays nil.
	metricsCtx, metricsCancel := context.WithTimeout(ctx, time.Second)
	usage := selected.fetchNodeUsage(metricsCtx)
	metricsCancel()
	name := c.name
	if c.id == h.reg.DefaultID() {
		if configured := selected.clusterName(ctx); configured != "" {
			name = configured
		}
	}
	view := clusterView{Nodes: make([]clusterNode, 0, len(nodes.Items)), Name: name, Total: len(nodes.Items)}
	for i := range nodes.Items {
		n := mapNode(&nodes.Items[i])
		if measured, ok := usage[n.Name]; ok {
			if n.CPU != nil {
				n.CPU.Used = &measured.cpuCores
			}
			if n.Memory != nil {
				n.Memory.Used = &measured.memoryBytes
			}
		}
		if n.Status == "Ready" {
			view.Ready++
		}
		view.Nodes = append(view.Nodes, n)
	}
	// Version is optional in clusterView. Discovery.ServerVersion has no
	// context argument, so fleet reads omit it rather than break their deadline.
	return &fleetInventory{Cluster: c.id, Name: name, View: view, Stats: clusterStats{
		Nodes: len(nodes.Items), TotalStorageBytes: nodeStorageCapacity(nodes.Items), UsedStorageBytes: boundVolumeBytes(pvs.Items),
	}}, nil
}

func (h fleetHandler) serverAccess(w http.ResponseWriter, req *http.Request) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	cl := scope.RequestedCluster(req)
	name := chi.URLParam(req, "name")
	obj, err := k.GetServer(req.Context(), ns, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	access := serverFleetAccess(u, cl, ns, obj)
	if !u.Can("servers:read", true, cl, ns) && !access.IsOwner && !access.IsCollaborator {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, struct {
		fleetServerAccess
		Target      fleetTarget `json:"target"`
		Permissions []string    `json:"permissions"`
	}{access, fleetTarget{Cluster: cl, Namespace: ns, Name: obj.GetName(), UID: string(obj.GetUID())}, fleetPermissions(u, cl, ns)})
}

func fleetPermissions(u *auth.User, cl, ns string) []string {
	permissions := make([]string, 0)
	for _, group := range rbac.Catalog {
		for _, p := range group.Permissions {
			if p.Namespaced && u.Can(p.Key, true, cl, ns) {
				permissions = append(permissions, p.Key)
			}
		}
	}
	sort.Strings(permissions)
	return permissions
}

func (h fleetHandler) placements(w http.ResponseWriter, req *http.Request) {
	u, f, ok := h.start(w, req)
	if !ok {
		return
	}
	out := fleetResult[fleetPlacement]{Items: make([]fleetPlacement, 0), Issues: make([]fleetIssue, 0)}
	ctx, cancel := context.WithTimeout(req.Context(), fleetTimeout)
	defer cancel()
	clusters, issues := h.candidates(ctx, f.cluster)
	out.Issues, out.Partial = issues, len(issues) > 0
	type eligible struct {
		cluster    fleetCluster
		namespaces []string
	}
	allowed := make([]eligible, 0)
	for _, c := range clusters {
		if !u.Can("templates:read", false, c.id, "") {
			continue
		}
		namespaces := make([]string, 0)
		for _, ns := range scope.AllowedNamespaces {
			if (f.namespace == "" || ns == f.namespace) && u.Can("servers:write", true, c.id, ns) {
				namespaces = append(namespaces, ns)
			}
		}
		if len(namespaces) > 0 {
			sort.Strings(namespaces)
			for _, ns := range namespaces {
				out.Scopes = append(out.Scopes, fleetScopeView{Cluster: c.id, Namespace: ns})
			}
			allowed = append(allowed, eligible{c, compactStrings(namespaces)})
		}
	}
	finishFleetScopes(&out)
	if len(allowed) > fleetMaxScopes {
		allowed = allowed[:fleetMaxScopes]
		out.Partial = true
		out.Issues = append(out.Issues, *fleetSafeIssue("", "", "limit"))
	}
	type templatesResult struct {
		templates []unstructured.Unstructured
		code      string
	}
	results := make([]templatesResult, len(allowed))
	var budget atomic.Int64
	budget.Store(fleetMaxScan)
	fleetParallel(len(allowed), func(i int) { results[i].templates, results[i].code = h.readTemplates(ctx, allowed[i].cluster, &budget) })
	remaining := fleetMaxItems
	for i, placement := range allowed {
		result := results[i]
		if result.code != "" {
			out.Partial = true
			out.Issues = append(out.Issues, *fleetSafeIssue(placement.cluster.id, "", result.code))
		}
		// An unavailable or empty catalog offers no valid game placement.
		if len(result.templates) == 0 {
			continue
		}
		for _, ns := range placement.namespaces {
			if len(out.Items) >= f.limit || remaining == 0 {
				out.Partial = true
				out.Issues = append(out.Issues, *fleetSafeIssue("", "", "limit"))
				out.TotalReturned = len(out.Items)
				writeJSON(w, out)
				return
			}
			templates := result.templates
			if len(templates) > remaining {
				templates = templates[:remaining]
				out.Partial = true
				out.Issues = append(out.Issues, *fleetSafeIssue(placement.cluster.id, ns, "limit"))
			}
			remaining -= len(templates)
			out.Items = append(out.Items, fleetPlacement{Cluster: placement.cluster.id, Namespace: ns, Templates: templates})
		}
	}
	out.TotalReturned = len(out.Items)
	writeJSON(w, out)
}

func (h fleetHandler) readTemplates(ctx context.Context, c fleetCluster, budget *atomic.Int64) ([]unstructured.Unstructured, string) {
	items := make([]unstructured.Unstructured, 0)
	if c.k == nil || c.k.Dynamic == nil {
		return items, "unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, fleetScopeTimeout)
	defer cancel()
	continuation := ""
	seen := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return items, "unavailable"
		}
		if len(items) >= fleetMaxItems || !reserveFleetPage(budget) {
			return items, "limit"
		}
		list, err := c.k.Dynamic.Resource(kube.GVRs["templates"]).List(ctx, metav1.ListOptions{Limit: fleetPageSize, Continue: continuation})
		if err != nil {
			return items, fleetErrorCode(err)
		}
		if len(list.Items) < fleetPageSize {
			budget.Add(int64(fleetPageSize - len(list.Items)))
		}
		for i := range min(len(list.Items), fleetPageSize) {
			obj := list.Items[i]
			if !seen[obj.GetName()] {
				seen[obj.GetName()] = true
				items = append(items, obj)
			}
		}
		if len(list.Items) > fleetPageSize {
			return items, "limit"
		}
		if list.GetContinue() == "" {
			sort.Slice(items, func(i, j int) bool { return items[i].GetName() < items[j].GetName() })
			return items, ""
		}
		if list.GetContinue() == continuation {
			return items, "limit"
		}
		continuation = list.GetContinue()
	}
}
