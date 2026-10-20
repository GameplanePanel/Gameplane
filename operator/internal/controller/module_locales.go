package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/GameplanePanel/gameplane/i18n/modlocale"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

const (
	// localesConfigMapSuffix names the ConfigMap that holds a module's
	// validated translations: <GameTemplate name> + this suffix.
	localesConfigMapSuffix = "-locales"
	// localesMaxBytes bounds the ConfigMap data (a ConfigMap is capped at
	// 1 MiB; this leaves headroom for metadata).
	localesMaxBytes = 900 * 1024
	// maxLocaleProblems caps the per-entry lines in one Warning event.
	maxLocaleProblems = 20
	// defaultLocalesNamespace is used when the reconciler has no namespace.
	defaultLocalesNamespace = "gameplane-system"

	// localesProblemsAnnotation records a fingerprint of the problem list last
	// reported on a GameTemplate, so a re-pull of the same bundle does not
	// repeat the Warning events. It is removed when the list is empty.
	localesProblemsAnnotation = "gameplane.local/locales-problems"

	eventReasonInvalidLocaleEntries = "InvalidLocaleEntries"
	eventReasonLocalesTooLarge      = "LocalesTooLarge"
	eventReasonLocalesWriteFailed   = "LocalesWriteFailed"

	// retryLocalesAfter is the requeue delay after a failed ConfigMap write.
	retryLocalesAfter = time.Minute
)

// localeOutcome is the result of validating a module's translations.
type localeOutcome struct {
	// Data maps "<tag>.json" to the marshalled valid entries.
	Data map[string]string
	// Problems lists "tag:id: problem" lines, sorted by tag then id.
	Problems []string
	// PerLang counts the dropped entries of each tag.
	PerLang map[string]int
	// Rejected lists the tags whose whole file was rejected (bad tag or a
	// malformed file).
	Rejected []string
	// Dropped lists the tags left out by the size bound.
	Dropped []string
	// Oversized lists the tags whose file was larger than
	// oci.MaxLocaleLayerSize, or did not fit oci.MaxLocaleTotalSize, and was
	// not read. They are never stored.
	Oversized []string
	// WriteErr is set when the translations ConfigMap could not be written
	// or deleted, so nothing new is being served.
	WriteErr error
}

// buildLocales validates every translation layer against the reference pack
// derived from the module's template. Invalid entries are dropped, never
// repaired; a file that does not parse is dropped whole. Languages are added
// in tag order until the next one would exceed localesMaxBytes; that one and
// every later one are left out.
func buildLocales(ref map[string]pack.Entry, layers map[string][]byte) localeOutcome {
	return buildLocalesWithOversized(ref, layers, nil)
}

// buildLocalesWithOversized is buildLocales plus the tags whose file was too
// large to read; they are recorded on the outcome, never stored.
func buildLocalesWithOversized(ref map[string]pack.Entry, layers map[string][]byte, oversized []string) localeOutcome {
	out := localeOutcome{Data: map[string]string{}, PerLang: map[string]int{}}
	// An oversized file was never read, but its name still comes from the
	// bundle: a name that is not a language tag is rejected like any other.
	for _, tag := range oversized {
		if !pack.ValidTag(tag) {
			out.Problems = append(out.Problems, fmt.Sprintf("%s:%s: invalid language tag", tag, pack.IDPack))
			out.Rejected = append(out.Rejected, tag)
			continue
		}
		out.Oversized = append(out.Oversized, tag)
	}
	sort.Strings(out.Oversized)
	tags := make([]string, 0, len(layers))
	for tag := range layers {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	used := 0
	full := false
	for _, tag := range tags {
		file := tag + ".json"
		if !pack.ValidTag(tag) {
			out.Problems = append(out.Problems, fmt.Sprintf("%s:%s: invalid language tag", tag, pack.IDPack))
			out.Rejected = append(out.Rejected, tag)
			continue
		}
		tr, err := pack.ParseEntries(layers[tag])
		if err != nil {
			out.Problems = append(out.Problems, fmt.Sprintf("%s:%s: %v", tag, pack.IDPack, err))
			out.Rejected = append(out.Rejected, tag)
			continue
		}
		bad := map[string]struct{}{}
		for _, f := range pack.ValidateTranslation(ref, tr, file) {
			if f.Severity != pack.SeverityError {
				continue
			}
			out.Problems = append(out.Problems, fmt.Sprintf("%s:%s: %s", tag, f.ID, f.Message))
			bad[f.ID] = struct{}{}
		}
		if len(bad) > 0 {
			out.PerLang[tag] = len(bad)
		}
		kept := pack.Valid(ref, tr)
		for id := range bad {
			delete(kept, id)
		}
		if len(kept) == 0 {
			continue
		}
		if full {
			out.Dropped = append(out.Dropped, tag)
			continue
		}
		data, err := pack.Marshal(kept)
		if err != nil {
			out.Problems = append(out.Problems, fmt.Sprintf("%s:%s: %v", tag, pack.IDPack, err))
			out.Rejected = append(out.Rejected, tag)
			continue
		}
		if used+len(file)+len(data) > localesMaxBytes {
			full = true
			out.Dropped = append(out.Dropped, tag)
			continue
		}
		used += len(file) + len(data)
		out.Data[file] = string(data)
	}
	return out
}

// localesCondition turns an outcome into the LocalesValid condition. The
// reason priority is WriteFailed (nothing new is served), LocalesTooLarge (whole
// languages are missing), InvalidEntries, then Valid.
func localesCondition(out localeOutcome) metav1.Condition {
	cond := localesConditionUnclipped(out)
	// Tags come from the bundle, so a hostile one could push the message past
	// the API limit and make every status patch fail.
	cond.Message = clipBytes(cond.Message, maxConditionMessageBytes)
	return cond
}

// maxConditionMessageBytes is the metav1.Condition message length limit.
const maxConditionMessageBytes = 32768

func localesConditionUnclipped(out localeOutcome) metav1.Condition {
	var parts []string
	if len(out.Oversized) > 0 {
		parts = append(parts, fmt.Sprintf("too large to read (over %d KiB per file or %d KiB for all languages together): %s", oci.MaxLocaleLayerSize/1024, oci.MaxLocaleTotalSize/1024, strings.Join(out.Oversized, ", ")))
	}
	if len(out.Dropped) > 0 {
		parts = append(parts, "left out because of the size limit: "+strings.Join(out.Dropped, ", "))
	}
	langs := make([]string, 0, len(out.PerLang))
	for tag := range out.PerLang {
		langs = append(langs, tag)
	}
	sort.Strings(langs)
	rejected := map[string]bool{}
	for _, tag := range out.Rejected {
		rejected[tag] = true
	}
	for _, tag := range langs {
		if rejected[tag] {
			continue
		}
		n := out.PerLang[tag]
		noun := "entries"
		if n == 1 {
			noun = "entry"
		}
		parts = append(parts, fmt.Sprintf("%s: %d %s dropped", tag, n, noun))
	}
	for _, tag := range out.Rejected {
		parts = append(parts, fmt.Sprintf("%s: file rejected", tag))
	}
	// The reasons are string literals: the condition-ids guard reads them from
	// the composite literals below.
	switch {
	case out.WriteErr != nil:
		parts = append([]string{"translations could not be stored: " + out.WriteErr.Error()}, parts...)
		return metav1.Condition{
			Type:    gameplanev1alpha1.GameTemplateConditionLocalesValid,
			Status:  metav1.ConditionFalse,
			Reason:  "WriteFailed",
			Message: strings.Join(parts, "; "),
		}
	case len(out.Dropped) > 0 || len(out.Oversized) > 0:
		return metav1.Condition{
			Type:    gameplanev1alpha1.GameTemplateConditionLocalesValid,
			Status:  metav1.ConditionFalse,
			Reason:  "LocalesTooLarge",
			Message: strings.Join(parts, "; "),
		}
	case len(parts) > 0:
		return metav1.Condition{
			Type:    gameplanev1alpha1.GameTemplateConditionLocalesValid,
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidEntries",
			Message: strings.Join(parts, "; "),
		}
	default:
		return metav1.Condition{
			Type:    gameplanev1alpha1.GameTemplateConditionLocalesValid,
			Status:  metav1.ConditionTrue,
			Reason:  "Valid",
			Message: "All module translations are valid.",
		}
	}
}

// problemsFingerprint identifies the problem list of an outcome: the first 12
// hex characters of a SHA-256 over its sorted lines. It is empty when there is
// nothing to report.
func problemsFingerprint(out localeOutcome) string {
	var lines []string
	lines = append(lines, out.Problems...)
	for _, tag := range out.Dropped {
		lines = append(lines, "dropped:"+tag)
	}
	for _, tag := range out.Oversized {
		lines = append(lines, "oversized:"+tag)
	}
	if out.WriteErr != nil {
		// The error text can vary between retries (timeouts, conflicts); a
		// failed write counts as one problem so retries do not repeat events.
		lines = append(lines, "write:failed")
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}

// tooLargeLanguages merges the oversized and dropped tags, sorted and unique.
func tooLargeLanguages(out localeOutcome) []string {
	seen := map[string]struct{}{}
	var tags []string
	for _, list := range [][]string{out.Oversized, out.Dropped} {
		for _, tag := range list {
			if _, dup := seen[tag]; dup {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	return tags
}

// problemEventNote joins up to maxLocaleProblems problem lines for an event.
func problemEventNote(problems []string) string {
	// events.k8s.io/v1 rejects a note over 1024 bytes, so lines are also
	// bounded by bytes: each line is clipped and the note stops early,
	// keeping room for the "... and N more" trailer.
	const maxLineBytes = 160
	const trailerReserve = 32
	var b strings.Builder
	shown := 0
	for _, line := range problems {
		if shown == maxLocaleProblems {
			break
		}
		line = clipBytes(line, maxLineBytes)
		if b.Len()+len(line)+1 > maxEventNoteBytes-trailerReserve {
			break
		}
		if shown > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		shown++
	}
	if rest := len(problems) - shown; rest > 0 {
		if shown > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "... and %d more", rest)
	}
	return b.String()
}

// maxEventNoteBytes is the events.k8s.io/v1 note length limit.
const maxEventNoteBytes = 1024

// clipBytes shortens s to at most n bytes without splitting a UTF-8 rune.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("...")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// localesReference derives the English reference pack of a bundle.
func localesReference(b *modsrc.Bundle) (map[string]pack.Entry, error) {
	var tmpl map[string]any
	if err := yaml.Unmarshal(b.TemplateYAML, &tmpl); err != nil {
		return nil, fmt.Errorf("parse template.yaml: %w", err)
	}
	module := map[string]any{
		"displayName": b.Metadata.DisplayName,
		"summary":     b.Metadata.Summary,
	}
	return modlocale.References(modlocale.Derive(module, tmpl)), nil
}

func (r *ModuleReconciler) localesNamespace() string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return defaultLocalesNamespace
}

// applyLocales validates the bundle's translation layers and publishes the
// valid entries in the "<template>-locales" ConfigMap, owned by the
// GameTemplate, in the operator namespace. It reports problems through the
// template's LocalesValid condition and Warning events, which fire only when
// the problem list changes. A translation problem never fails a module
// install; the only error returned is a failed ConfigMap write or delete, so
// the caller can retry later.
func (r *ModuleReconciler) applyLocales(ctx context.Context, tmpl *gameplanev1alpha1.GameTemplate, b *modsrc.Bundle) error {
	if tmpl == nil || b == nil {
		return nil
	}
	logger := log.FromContext(ctx).WithValues("template", tmpl.Name)
	key := types.NamespacedName{Namespace: r.localesNamespace(), Name: tmpl.Name + localesConfigMapSuffix}

	if len(b.Locales) == 0 && len(b.OversizedLocales) == 0 {
		if err := r.deleteLocalesConfigMap(ctx, key); err != nil {
			// Stale translations are still served: report WriteFailed
			// instead of clearing the condition.
			logger.Error(err, "unable to remove stale module translations")
			return r.reportLocales(ctx, tmpl, localeOutcome{WriteErr: err})
		}
		if err := r.setLocalesCondition(ctx, tmpl, nil); err != nil {
			logger.Error(err, "unable to clear the LocalesValid condition")
		}
		if _, err := r.setLocalesFingerprint(ctx, tmpl, ""); err != nil {
			logger.Error(err, "unable to clear the locales problems fingerprint")
		}
		return nil
	}

	ref, err := localesReference(b)
	if err != nil {
		logger.Error(err, "unable to derive module translation ids")
		return nil
	}
	out := buildLocalesWithOversized(ref, b.Locales, b.OversizedLocales)

	if len(out.Data) == 0 {
		err = r.deleteLocalesConfigMap(ctx, key)
	} else {
		err = r.writeLocalesConfigMap(ctx, tmpl, key, out.Data)
	}
	if err != nil {
		logger.Error(err, "unable to publish module translations")
		out.WriteErr = err
	}
	return r.reportLocales(ctx, tmpl, out)
}

// reportLocales records out on the template's LocalesValid condition and, when
// the problem list changed, emits the Warning events. It returns out.WriteErr.
func (r *ModuleReconciler) reportLocales(ctx context.Context, tmpl *gameplanev1alpha1.GameTemplate, out localeOutcome) error {
	logger := log.FromContext(ctx).WithValues("template", tmpl.Name)
	cond := localesCondition(out)
	cond.ObservedGeneration = tmpl.Generation
	if err := r.setLocalesCondition(ctx, tmpl, &cond); err != nil {
		logger.Error(err, "unable to record the LocalesValid condition")
	}
	changed, err := r.setLocalesFingerprint(ctx, tmpl, problemsFingerprint(out))
	if err != nil {
		// Without a stored fingerprint every re-pull would look new, so stay quiet.
		logger.Error(err, "unable to record the locales problems fingerprint")
		return out.WriteErr
	}
	if !changed {
		return out.WriteErr
	}
	if len(out.Problems) > 0 {
		r.recordLocaleEvent(tmpl, corev1.EventTypeWarning, eventReasonInvalidLocaleEntries, problemEventNote(out.Problems))
	}
	if tags := tooLargeLanguages(out); len(tags) > 0 {
		r.recordLocaleEvent(tmpl, corev1.EventTypeWarning, eventReasonLocalesTooLarge,
			clipBytes("translations too large or left out because of the size limit: "+strings.Join(tags, ", "), maxEventNoteBytes))
	}
	if out.WriteErr != nil {
		r.recordLocaleEvent(tmpl, corev1.EventTypeWarning, eventReasonLocalesWriteFailed,
			clipBytes("translations could not be stored: "+out.WriteErr.Error(), maxEventNoteBytes))
	}
	return out.WriteErr
}

// setLocalesFingerprint stores fp in the template's problems annotation (or
// removes the annotation when fp is empty). It reports whether the stored
// value changed, which is when the problem list changed and events are due.
func (r *ModuleReconciler) setLocalesFingerprint(ctx context.Context, tmpl *gameplanev1alpha1.GameTemplate, fp string) (bool, error) {
	prev := tmpl.Annotations[localesProblemsAnnotation]
	if prev == fp {
		return false, nil
	}
	base := tmpl.DeepCopy()
	if fp == "" {
		delete(tmpl.Annotations, localesProblemsAnnotation)
	} else {
		if tmpl.Annotations == nil {
			tmpl.Annotations = map[string]string{}
		}
		tmpl.Annotations[localesProblemsAnnotation] = fp
	}
	if err := r.Patch(ctx, tmpl, client.MergeFrom(base)); err != nil {
		return false, fmt.Errorf("patch GameTemplate %s annotations: %w", tmpl.Name, err)
	}
	return fp != "", nil
}

func (r *ModuleReconciler) writeLocalesConfigMap(ctx context.Context, tmpl *gameplanev1alpha1.GameTemplate, key types.NamespacedName, data map[string]string) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = data
		return controllerutil.SetControllerReference(tmpl, cm, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("write ConfigMap %s: %w", key, err)
	}
	return nil
}

func (r *ModuleReconciler) deleteLocalesConfigMap(ctx context.Context, key types.NamespacedName) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete ConfigMap %s: %w", key, err)
	}
	return nil
}

// setLocalesCondition records cond on the template's status, or removes the
// condition when cond is nil. It patches only when something changes.
func (r *ModuleReconciler) setLocalesCondition(ctx context.Context, tmpl *gameplanev1alpha1.GameTemplate, cond *metav1.Condition) error {
	base := tmpl.DeepCopy()
	if cond == nil {
		if meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid) == nil {
			return nil
		}
		meta.RemoveStatusCondition(&tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	} else {
		tmpl.Status.Conditions = upsertCondition(tmpl.Status.Conditions, *cond)
		if sameConditions(base.Status.Conditions, tmpl.Status.Conditions) {
			return nil
		}
	}
	if err := r.Status().Patch(ctx, tmpl, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch GameTemplate %s status: %w", tmpl.Name, err)
	}
	return nil
}

// recordLocaleEvent emits an event on the template when an EventRecorder is
// configured; it is a no-op otherwise.
func (r *ModuleReconciler) recordLocaleEvent(tmpl *gameplanev1alpha1.GameTemplate, eventType, reason, note string) {
	if r.EventRecorder == nil {
		return
	}
	r.EventRecorder.Eventf(tmpl, nil, eventType, reason, reason, "%s", note)
}
