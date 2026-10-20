package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
	"sigs.k8s.io/yaml"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
	"github.com/GameplanePanel/gameplane/operator/internal/verify"
)

// moduleFailedRetryInterval paces retries of a Failed Module. A spec change
// fixes most failure causes (a re-pin, a corrected digest); it bumps the
// generation and reconciles at once through the Module watch. A ModuleSource
// change (new catalog, new verify policy) enqueues the Module through
// enqueueModulesForSource. This timer only covers causes that clear with
// neither, such as a registry coming back up, so it reuses the ModuleSource
// refresh floor instead of hot-looping (F-258).
const moduleFailedRetryInterval = minRefreshInterval

// ModuleReconciler materializes Module CRs into GameTemplate CRs. The
// produced GameTemplate carries an OwnerReference back to the Module so
// uninstall = `kubectl delete module <name>` and the K8s GC reaps the
// template.
type ModuleReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string

	// OperatorVersion is this operator's build version, compared against a
	// bundle's gameplaneMinVersion to refuse modules that need a newer
	// operator. Empty or "dev" disables the check.
	OperatorVersion string

	// FetchOptions carries operator-level fetcher config (CLI flags).
	FetchOptions modsrc.Options

	// NewFetcher is overridden in tests with an in-process fake. nil →
	// the real per-source-type fetcher from modsrc.ForSource.
	NewFetcher func(ctx context.Context, src *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error)

	// NewVerifier is overridden in tests with an in-process fake. nil →
	// the real cosign verifier from verify.Build.
	NewVerifier func(ctx context.Context, src *gameplanev1alpha1.ModuleSource) (verify.Verifier, error)

	// EventRecorder surfaces module translation problems as Warning events
	// on the GameTemplate. nil disables events (unit tests).
	EventRecorder recorder.EventRecorder
}

// +kubebuilder:rbac:groups=gameplane.local,resources=modules,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gameplane.local,resources=modules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gameplane.local,resources=modules/finalizers,verbs=update
// +kubebuilder:rbac:groups=gameplane.local,resources=gametemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gameplane.local,resources=gameservers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *ModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return requeueOnConflict(r.reconcile(ctx, req))
}

func (r *ModuleReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var mod gameplanev1alpha1.Module
	if err := r.Get(ctx, req.NamespacedName, &mod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !mod.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &mod)
	}
	if !controllerutil.ContainsFinalizer(&mod, gameplanev1alpha1.ModuleFinalizer) {
		controllerutil.AddFinalizer(&mod, gameplanev1alpha1.ModuleFinalizer)
		if err := r.Update(ctx, &mod); err != nil {
			return ctrl.Result{}, err
		}
		// Re-queue from the watch event after the finalizer add.
		return ctrl.Result{}, nil
	}

	// Resolve which version to install.
	src, err := r.getSource(ctx, mod.Spec.Source.Name)
	if err != nil {
		return r.markFailed(ctx, &mod, "SourceNotFound", err)
	}
	entry := byCatalogName(src.Status.Modules, mod.Spec.Name)
	if entry == nil {
		return r.markPending(ctx, &mod, "WaitingForCatalog",
			fmt.Errorf("source %q has not yet indexed module %q", src.Name, mod.Spec.Name))
	}
	desiredVersion := mod.Spec.Version
	if desiredVersion == "" {
		desiredVersion = entry.LatestVersion
	}
	if desiredVersion == "" {
		return r.markPending(ctx, &mod, "NoVersionAvailable",
			fmt.Errorf("source %q has no available versions for module %q", src.Name, mod.Spec.Name))
	}
	if !slices.Contains(entry.Versions, desiredVersion) {
		return r.markFailed(ctx, &mod, "VersionUnavailable",
			fmt.Errorf("version %q not in catalog for %q (available: %v)",
				desiredVersion, mod.Spec.Name, entry.Versions))
	}

	if mod.Status.AppliedVersion == desiredVersion && mod.Status.AppliedTemplate == mod.Name &&
		mod.Status.Phase == gameplanev1alpha1.ModulePhaseReady &&
		// The same version in a different source or logical module is not the
		// installed bundle. Every desired-state change must pass through apply.
		mod.Status.ObservedGeneration == mod.Generation &&
		// entry.Digest describes only the catalog's LatestVersion, never a
		// pinned older one, so it can only gate convergence when the
		// desired version *is* the latest — otherwise a pinned install
		// could never match it and would flap Pulling/Ready forever
		// (F-046).
		(desiredVersion != entry.LatestVersion || entry.Digest == "" || mod.Status.AppliedDigest == entry.Digest) &&
		(mod.Spec.Digest == "" || mod.Status.AppliedDigest == mod.Spec.Digest) {
		// Already converged. Non-OCI sources publish a single version
		// stream, so the digest comparison is what catches content
		// changes hiding behind an unchanged version string. A set
		// spec.digest must also match the applied content, so a pin
		// added or changed on a Ready Module goes through the pin check
		// below instead of returning here.
		//
		// Status alone isn't proof the owned GameTemplate is actually
		// there: a kubectl-deleted managed template (F-050) leaves these
		// fields untouched, so confirm it still exists before trusting
		// them. Recreating it needs the bundle content again, so a miss
		// falls through to the normal pull/apply path below instead of
		// returning here.
		var tmpl gameplanev1alpha1.GameTemplate
		err := r.Get(ctx, types.NamespacedName{Name: mod.Status.AppliedTemplate}, &tmpl)
		if err == nil {
			return ctrl.Result{}, nil
		}
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		log.FromContext(ctx).Info("owned GameTemplate missing for a converged Module; recreating",
			"template", mod.Status.AppliedTemplate)
	}

	// Pull bundle.
	fetcher, err := r.fetcherFor(ctx, src)
	if err != nil {
		return r.markFailed(ctx, &mod, "SourceConfig", err)
	}

	if err := r.markPullingTransition(ctx, &mod, desiredVersion); err != nil {
		return ctrl.Result{}, err
	}
	bundle, err := fetcher.Pull(ctx, mod.Spec.Name, desiredVersion)
	if err != nil {
		return r.markFailed(ctx, &mod, "PullFailed", err)
	}

	// Verify the bundle's signature before trusting any of its content —
	// including the metadata read below. Nop when the source declares no
	// verify policy.
	verifier, err := r.verifierFor(ctx, src)
	if err != nil {
		return r.markFailed(ctx, &mod, "VerifyConfig", err)
	}
	if err := verifier.Verify(ctx, entry.Reference, bundle.Digest); err != nil {
		return r.markFailed(ctx, &mod, "SignatureInvalid", err)
	}

	// Honor a content pin: refuse a bundle whose digest doesn't match the
	// one the user pinned (catches a tag moved to new content).
	if mod.Spec.Digest != "" && bundle.Digest != mod.Spec.Digest {
		return r.markFailed(ctx, &mod, "DigestMismatch",
			fmt.Errorf("pinned digest %s but resolved bundle is %s", mod.Spec.Digest, bundle.Digest))
	}

	// Refuse a bundle that needs a newer operator than this one — the
	// reconciler can't honor capabilities it doesn't understand. Leaving the
	// previously-applied GameTemplate untouched here is intentional.
	if r.operatorTooOld(bundle.Metadata.GameplaneMinVersion) {
		return r.markFailed(ctx, &mod, "IncompatibleOperator",
			fmt.Errorf("module %q requires Gameplane >= %s but this operator is %s",
				mod.Spec.Name, bundle.Metadata.GameplaneMinVersion, r.OperatorVersion))
	}

	// Materialize a GameTemplate.
	tmpl, err := r.applyTemplate(ctx, &mod, bundle, desiredVersion, src.Name)
	if err != nil {
		return r.markFailed(ctx, &mod, "ApplyTemplate", err)
	}
	// Translation problems never fail the install (FR-034). A failed
	// ConfigMap write is retried a minute later.
	var localesRetry time.Duration
	if err := r.applyLocales(ctx, tmpl, bundle); err != nil {
		log.FromContext(ctx).Error(err, "module translations not stored; will retry", "module", mod.Name)
		localesRetry = retryLocalesAfter
	}

	// Update Status to Ready. Record the version being replaced as the
	// rollback target before overwriting it (only when it actually changes).
	if mod.Status.AppliedVersion != "" && mod.Status.AppliedVersion != desiredVersion {
		mod.Status.PreviousVersion = mod.Status.AppliedVersion
		mod.Status.PreviousDigest = mod.Status.AppliedDigest
	}
	mod.Status.Phase = gameplanev1alpha1.ModulePhaseReady
	mod.Status.AppliedVersion = desiredVersion
	mod.Status.AppliedDigest = bundle.Digest
	mod.Status.AppliedTemplate = mod.Name
	// Record the verification that actually ran, not just that Verify()
	// returned nil — verifierFor returns a Nop verifier (always nil) when
	// the source declares no policy, so gate the record on the policy
	// itself. Otherwise a source that later gains a verify policy would
	// make every module already installed from it look "verified" even
	// though nothing was ever signature-checked.
	if mode := moduleVerifyMode(src.Spec.Verify); mode != "" {
		mod.Status.VerifiedDigest = bundle.Digest
		mod.Status.VerifyPolicy = mode
	} else {
		mod.Status.VerifiedDigest = ""
		mod.Status.VerifyPolicy = ""
	}
	mod.Status.LastError = ""
	mod.Status.ObservedGeneration = mod.Generation
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Applied",
		Message:            fmt.Sprintf("GameTemplate %q at %s", mod.Name, desiredVersion),
		ObservedGeneration: mod.Generation,
	})
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionPulling,
		Status:             metav1.ConditionFalse,
		Reason:             "Applied",
		ObservedGeneration: mod.Generation,
	})
	if err := r.Status().Update(ctx, &mod); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: localesRetry}, nil
}

func (r *ModuleReconciler) applyTemplate(ctx context.Context, mod *gameplanev1alpha1.Module, bundle *modsrc.Bundle, version, sourceName string) (*gameplanev1alpha1.GameTemplate, error) {
	parsed := &gameplanev1alpha1.GameTemplate{}
	if err := yaml.Unmarshal(bundle.TemplateYAML, parsed); err != nil {
		return nil, fmt.Errorf("parse template.yaml: %w", err)
	}

	// Legacy bundles declare a singular spec.category; the typed GameTemplate
	// no longer has that field, so recover it here and fold it into Categories.
	// Mirrors modsrc.Metadata.normalizeCategories for module.yaml.
	var legacy struct {
		Spec struct {
			Category string `json:"category"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(bundle.TemplateYAML, &legacy); err == nil {
		if len(parsed.Spec.Categories) == 0 && legacy.Spec.Category != "" {
			parsed.Spec.Categories = []string{legacy.Spec.Category}
		}
	}

	desired := &gameplanev1alpha1.GameTemplate{}
	desired.Name = mod.Name
	desired.Spec = parsed.Spec

	// Stamp module-management labels/annotations on the GameTemplate
	// metadata so the API can distinguish managed vs. manual templates.
	if desired.Labels == nil {
		desired.Labels = map[string]string{}
	}
	desired.Labels[gameplanev1alpha1.LabelManagedBy] = gameplanev1alpha1.ManagedByModule
	desired.Labels[gameplanev1alpha1.LabelModuleName] = mod.Spec.Name
	desired.Labels[gameplanev1alpha1.LabelModuleVersion] = version
	desired.Labels[gameplanev1alpha1.LabelModuleSource] = sourceName
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[gameplanev1alpha1.LabelModuleDigest] = bundle.Digest

	// Set the OwnerReference so deleting the Module GCs the template.
	if err := controllerutil.SetControllerReference(mod, desired, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner ref: %w", err)
	}

	// Server-side apply or create-or-update via SSA-like pattern. We do
	// a Get + decide create/update because some tests assert on the
	// resulting object's fields and we want predictable behavior.
	var existing gameplanev1alpha1.GameTemplate
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name}, &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return nil, err
		}
		return desired, nil
	}
	if err != nil {
		return nil, err
	}
	// Verify ownership before clobbering — refuse to mutate a template
	// that wasn't created by this Module (e.g. pre-existing manual
	// install with the same name).
	if existing.Labels[gameplanev1alpha1.LabelManagedBy] != gameplanev1alpha1.ManagedByModule {
		return nil, fmt.Errorf("template %q exists and is not module-managed", desired.Name)
	}
	if !ownedBy(&existing, mod) {
		return nil, fmt.Errorf("template %q is owned by a different Module", desired.Name)
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = mergeAnnotations(existing.Annotations, desired.Annotations)
	if err := r.Update(ctx, &existing); err != nil {
		return nil, err
	}
	return &existing, nil
}

// finalize handles the deletion path. We refuse to release the
// finalizer (and therefore allow GC of the GameTemplate) while any
// GameServer references it — uninstall would orphan running pods.
func (r *ModuleReconciler) finalize(ctx context.Context, mod *gameplanev1alpha1.Module) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(mod, gameplanev1alpha1.ModuleFinalizer) {
		return ctrl.Result{}, nil
	}
	tmplName := mod.Status.AppliedTemplate
	if tmplName == "" {
		tmplName = mod.Name
	}
	var servers gameplanev1alpha1.GameServerList
	if err := r.List(ctx, &servers); err != nil {
		return ctrl.Result{}, err
	}
	var inUse []string
	for i := range servers.Items {
		if servers.Items[i].Spec.TemplateRef.Name == tmplName {
			inUse = append(inUse, servers.Items[i].Namespace+"/"+servers.Items[i].Name)
		}
	}
	if len(inUse) > 0 {
		// Surface a clear blocker on status; the API translates this
		// into a 409 on DELETE /modules/{name} so the UI can render a
		// useful error.
		mod.Status.Phase = gameplanev1alpha1.ModulePhaseFailed
		mod.Status.LastError = fmt.Sprintf("GameTemplate %q is still in use by: %v", tmplName, inUse)
		mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
			Type:               gameplanev1alpha1.ModuleConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "InUse",
			Message:            mod.Status.LastError,
			ObservedGeneration: mod.Generation,
		})
		if err := r.Status().Update(ctx, mod); err != nil {
			return ctrl.Result{}, err
		}
		// Don't release the finalizer; requeue so we re-check after the
		// user removes the GameServers.
		return ctrl.Result{Requeue: true}, nil
	}

	// Delete the materialized GameTemplate. SetControllerReference would
	// also let the K8s GC do this, but we don't rely on the GC ordering
	// because the Module disappears first.
	tmpl := &gameplanev1alpha1.GameTemplate{}
	if err := r.Get(ctx, types.NamespacedName{Name: tmplName}, tmpl); err == nil {
		if ownedBy(tmpl, mod) {
			if err := r.Delete(ctx, tmpl); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("delete owned template: %w", err)
			}
		}
	}
	controllerutil.RemoveFinalizer(mod, gameplanev1alpha1.ModuleFinalizer)
	if err := r.Update(ctx, mod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *ModuleReconciler) getSource(ctx context.Context, name string) (*gameplanev1alpha1.ModuleSource, error) {
	var src gameplanev1alpha1.ModuleSource
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &src); err != nil {
		return nil, err
	}
	return &src, nil
}

func (r *ModuleReconciler) markPending(ctx context.Context, mod *gameplanev1alpha1.Module, reason string, err error) (ctrl.Result, error) {
	before := mod.Status.DeepCopy()
	mod.Status.Phase = gameplanev1alpha1.ModulePhasePending
	mod.Status.LastError = err.Error()
	mod.Status.ObservedGeneration = mod.Generation
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: mod.Generation,
	})
	// Same rationale as markFailed: a pulling reconcile can be interrupted
	// (e.g. the catalog drops the module/version) and land here instead,
	// so clear any stale Pulling=True left over from that attempt —
	// otherwise Phase=Pending could coexist with a Pulling condition
	// stuck True forever.
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionPulling,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		ObservedGeneration: mod.Generation,
	})
	if uerr := r.updateStatusIfChanged(ctx, mod, before); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *ModuleReconciler) markFailed(ctx context.Context, mod *gameplanev1alpha1.Module, reason string, err error) (ctrl.Result, error) {
	before := mod.Status.DeepCopy()
	mod.Status.Phase = gameplanev1alpha1.ModulePhaseFailed
	mod.Status.LastError = err.Error()
	mod.Status.ObservedGeneration = mod.Generation
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: mod.Generation,
	})
	// A failure terminates the pull attempt. markPullingTransition sets
	// Pulling=True before the steps that fail into here (pull, verify,
	// digest, apply), so clear it — otherwise the dashboard shows a module
	// stuck "Pulling" forever alongside "Failed".
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionPulling,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		ObservedGeneration: mod.Generation,
	})
	// Skip the write when the Module is already Failed at this generation
	// for the same cause. Every status write is a watch event that queues
	// another reconcile, so rewriting an unchanged Failed status would
	// hot-loop the Module and make other writers (dashboard, kubectl)
	// conflict with it.
	if uerr := r.updateStatusIfChanged(ctx, mod, before); uerr != nil {
		return ctrl.Result{}, uerr
	}
	// Pace the retry with a timer rather than returning err: the failure is
	// recorded on status, and it must not depend on its own status write to
	// come round again. Spec and ModuleSource changes still reconcile at once.
	log.FromContext(ctx).Info("module install failed; will retry",
		"reason", reason, "error", err.Error(), "retryAfter", moduleFailedRetryInterval.String())
	return ctrl.Result{RequeueAfter: moduleFailedRetryInterval}, nil
}

func (r *ModuleReconciler) markPullingTransition(ctx context.Context, mod *gameplanev1alpha1.Module, version string) error {
	if mod.Status.Phase == gameplanev1alpha1.ModulePhasePulling {
		return nil
	}
	// A retry of a Module that already failed at this generation re-pulls
	// quietly and keeps showing Failed. Flipping it to Pulling would write
	// status twice per retry (Failed to Pulling here, then back to Failed in
	// markFailed when the cause persists), and each write re-queues the
	// Module through its own watch (F-258). If the retry succeeds, the Ready
	// write still lands; if it fails for a different cause, markFailed
	// records that. A spec change bumps the generation, so a re-pin still
	// shows Pulling.
	if mod.Status.Phase == gameplanev1alpha1.ModulePhaseFailed &&
		mod.Status.ObservedGeneration == mod.Generation {
		return nil
	}
	mod.Status.Phase = gameplanev1alpha1.ModulePhasePulling
	// Starting a fresh pull supersedes any prior failure — clear the stale
	// error so the status doesn't report a "Pulling" phase alongside a
	// leftover LastError (e.g. a re-pin away from an unavailable version would
	// otherwise keep showing "version X not in catalog" until Ready).
	mod.Status.LastError = ""
	mod.Status.Conditions = upsertCondition(mod.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ModuleConditionPulling,
		Status:             metav1.ConditionTrue,
		Reason:             "Pulling",
		Message:            "pulling " + version,
		ObservedGeneration: mod.Generation,
	})
	return r.Status().Update(ctx, mod)
}

// updateStatusIfChanged writes mod.Status only when it differs from before
// in more than condition LastTransitionTimes. An unchanged status is not
// written, so it raises no watch event and no resourceVersion bump.
func (r *ModuleReconciler) updateStatusIfChanged(ctx context.Context, mod *gameplanev1alpha1.Module, before *gameplanev1alpha1.ModuleStatus) error {
	if !moduleStatusChanged(before, &mod.Status) {
		return nil
	}
	return r.Status().Update(ctx, mod)
}

// moduleStatusChanged reports whether b differs from a, ignoring condition
// LastTransitionTimes.
func moduleStatusChanged(a, b *gameplanev1alpha1.ModuleStatus) bool {
	if !sameConditions(a.Conditions, b.Conditions) {
		return true
	}
	ac, bc := a.DeepCopy(), b.DeepCopy()
	ac.Conditions, bc.Conditions = nil, nil
	return !equality.Semantic.DeepEqual(ac, bc)
}

func (r *ModuleReconciler) fetcherFor(ctx context.Context, src *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error) {
	if r.NewFetcher != nil {
		return r.NewFetcher(ctx, src)
	}
	return modsrc.ForSource(ctx, r.Client, r.Namespace, src, r.FetchOptions)
}

func (r *ModuleReconciler) verifierFor(ctx context.Context, src *gameplanev1alpha1.ModuleSource) (verify.Verifier, error) {
	if r.NewVerifier != nil {
		return r.NewVerifier(ctx, src)
	}
	return verify.Build(ctx, r.Client, r.Namespace, src)
}

// moduleVerifyMode classifies a source's verify policy for status recording,
// mirroring the web client's verifyMode (web/src/lib/verify.ts), which
// prefers keyless over keyed. This checks keyless before keyed, the reverse
// of verify.Build's key-before-keyless switch; the two never disagree in
// practice because the CRD's XValidation rule requires exactly one of
// Key/Keyless. Empty means the source declares no policy, so verifierFor
// returns the always-succeeding Nop verifier — no real signature check
// happened, and nothing should be recorded as verified.
func moduleVerifyMode(spec *gameplanev1alpha1.VerifySpec) string {
	if spec == nil {
		return ""
	}
	if spec.Keyless != nil {
		return gameplanev1alpha1.ModuleVerifyModeKeyless
	}
	if spec.Key != nil {
		return gameplanev1alpha1.ModuleVerifyModeKeyed
	}
	return ""
}

// operatorTooOld reports whether minVersion (a bundle's gameplaneMinVersion)
// is newer than this operator. It is conservative: an empty requirement, a
// "dev"/empty operator build, or either value failing to parse as semver all
// skip the gate so local and pre-release clusters keep working.
func (r *ModuleReconciler) operatorTooOld(minVersion string) bool {
	if minVersion == "" || r.OperatorVersion == "" || r.OperatorVersion == "dev" {
		return false
	}
	have := "v" + strings.TrimPrefix(r.OperatorVersion, "v")
	want := "v" + strings.TrimPrefix(minVersion, "v")
	if !semver.IsValid(have) || !semver.IsValid(want) {
		return false
	}
	return semver.Compare(have, want) < 0
}

func byCatalogName(entries []gameplanev1alpha1.ModuleEntry, name string) *gameplanev1alpha1.ModuleEntry {
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	return nil
}

func ownedBy(o client.Object, owner *gameplanev1alpha1.Module) bool {
	for _, ref := range o.GetOwnerReferences() {
		if ref.UID == owner.UID && ref.Kind == "Module" {
			return true
		}
	}
	return false
}

func mergeAnnotations(into, from map[string]string) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	for k, v := range from {
		into[k] = v
	}
	return into
}

func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.Module{}).
		Owns(&gameplanev1alpha1.GameTemplate{}).
		Watches(&gameplanev1alpha1.ModuleSource{}, enqueueModulesForSource(r.Client)).
		Complete(r)
}

// enqueueModulesForSource maps a ModuleSource change to a reconcile of
// every Module that references it — so once the source's catalog
// indexes, pending Modules can resolve their version.
func enqueueModulesForSource(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		src, ok := obj.(*gameplanev1alpha1.ModuleSource)
		if !ok {
			return nil
		}
		var mods gameplanev1alpha1.ModuleList
		if err := c.List(ctx, &mods); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for i := range mods.Items {
			if mods.Items[i].Spec.Source.Name == src.Name {
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: mods.Items[i].Name},
				})
			}
		}
		return reqs
	})
}
