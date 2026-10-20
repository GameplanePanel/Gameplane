package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/GameplanePanel/gameplane/i18n/pack"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
)

func localesRef() map[string]pack.Entry {
	return map[string]pack.Entry{
		"template.displayName": {DefaultMessage: "Minecraft", Description: "d"},
		"module.summary":       {DefaultMessage: "Summary {n}", Description: "d"},
	}
}

func TestBuildLocales_ValidKept(t *testing.T) {
	out := buildLocales(localesRef(), map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Minecraft FR"}}`),
	})
	if len(out.Problems) != 0 || len(out.Dropped) != 0 || len(out.Rejected) != 0 {
		t.Fatalf("unexpected problems: %+v", out)
	}
	got, err := pack.ParseEntries([]byte(out.Data["fr.json"]))
	if err != nil || got["template.displayName"].DefaultMessage != "Minecraft FR" {
		t.Fatalf("fr.json = %q, %v", out.Data["fr.json"], err)
	}
	if status := localesCondition(out); status.Status != metav1.ConditionTrue || status.Reason != "Valid" {
		t.Errorf("condition = %+v", status)
	}
}

func TestBuildLocales_DropsInvalidEntries(t *testing.T) {
	out := buildLocales(localesRef(), map[string][]byte{
		"fr": []byte(`{
			"template.displayName":{"defaultMessage":"Bon"},
			"module.summary":{"defaultMessage":"{extra}"},
			"unknown.id":{"defaultMessage":"x"}
		}`),
	})
	got, err := pack.ParseEntries([]byte(out.Data["fr.json"]))
	if err != nil || len(got) != 1 || got["template.displayName"].DefaultMessage != "Bon" {
		t.Fatalf("fr.json = %q, %v", out.Data["fr.json"], err)
	}
	if out.PerLang["fr"] != 2 || len(out.Problems) < 2 {
		t.Fatalf("outcome = %+v", out)
	}
	for _, p := range out.Problems {
		if !strings.HasPrefix(p, "fr:") {
			t.Errorf("problem %q lacks the tag prefix", p)
		}
	}
	c := localesCondition(out)
	if c.Status != metav1.ConditionFalse || c.Reason != "InvalidEntries" || c.Message != "fr: 2 entries dropped" {
		t.Errorf("condition = %+v", c)
	}
}

func TestBuildLocales_SingularCountAndRejectedFiles(t *testing.T) {
	out := buildLocales(localesRef(), map[string][]byte{
		"de":  []byte(`{"unknown.id":{"defaultMessage":"x"}}`),
		"xx":  []byte(`not json`),
		"BAD": []byte(`{}`),
	})
	if len(out.Data) != 0 {
		t.Fatalf("Data = %v", out.Data)
	}
	c := localesCondition(out)
	if !strings.Contains(c.Message, "de: 1 entry dropped") || !strings.Contains(c.Message, "xx: file rejected") ||
		!strings.Contains(c.Message, "BAD: file rejected") {
		t.Errorf("message = %q", c.Message)
	}
	joined := strings.Join(out.Problems, "\n")
	if !strings.Contains(joined, "BAD:<pack>: invalid language tag") {
		t.Errorf("problems = %q", joined)
	}
}

func TestBuildLocales_DeterministicOrder(t *testing.T) {
	layers := map[string][]byte{
		"fr": []byte(`{"unknown.b":{"defaultMessage":"x"},"unknown.a":{"defaultMessage":"x"}}`),
		"de": []byte(`{"unknown.c":{"defaultMessage":"x"}}`),
	}
	first := buildLocales(localesRef(), layers).Problems
	for range 10 {
		again := buildLocales(localesRef(), layers).Problems
		if strings.Join(again, "|") != strings.Join(first, "|") {
			t.Fatalf("non-deterministic problems:\n%v\n%v", first, again)
		}
	}
	if !strings.HasPrefix(first[0], "de:") || !strings.HasPrefix(first[1], "fr:unknown.a") {
		t.Errorf("problems not sorted by tag then id: %v", first)
	}
}

func TestBuildLocales_SizeBoundStopsAtFirstOverflow(t *testing.T) {
	big := strings.Repeat("x", 400*1024)
	mk := func() []byte {
		return []byte(fmt.Sprintf(`{"template.displayName":{"defaultMessage":%q}}`, big))
	}
	ref := map[string]pack.Entry{"template.displayName": {DefaultMessage: "Minecraft", Description: "d"}}
	out := buildLocales(ref, map[string][]byte{"de": mk(), "fr": mk(), "it": mk(), "nl": []byte(`{"template.displayName":{"defaultMessage":"s"}}`)})
	if _, ok := out.Data["de.json"]; !ok {
		t.Error("de.json should be kept")
	}
	if _, ok := out.Data["fr.json"]; !ok {
		t.Error("fr.json should be kept")
	}
	if len(out.Dropped) != 2 || out.Dropped[0] != "it" || out.Dropped[1] != "nl" {
		t.Errorf("Dropped = %v; the small nl must not slip in after the overflow", out.Dropped)
	}
	c := localesCondition(out)
	if c.Reason != "LocalesTooLarge" || !strings.Contains(c.Message, "it, nl") {
		t.Errorf("condition = %+v", c)
	}
}

func TestProblemEventNote_Truncates(t *testing.T) {
	var problems []string
	for i := range 25 {
		problems = append(problems, fmt.Sprintf("fr:id%d: bad", i))
	}
	lines := strings.Split(problemEventNote(problems), "\n")
	if len(lines) != maxLocaleProblems+1 || lines[len(lines)-1] != "... and 5 more" {
		t.Fatalf("lines = %d, last = %q", len(lines), lines[len(lines)-1])
	}
	if got := problemEventNote(problems[:3]); strings.Contains(got, "more") {
		t.Errorf("short note truncated: %q", got)
	}
}

func localesBundle(layers map[string][]byte) *modsrc.Bundle {
	return &modsrc.Bundle{
		Metadata:     modsrc.Metadata{Name: "demo", DisplayName: "Demo", Summary: "Summary"},
		TemplateYAML: []byte("spec:\n  displayName: Demo Game\n"),
		Locales:      layers,
	}
}

func newLocalesFixture(t *testing.T, rec *events.FakeRecorder) (*ModuleReconciler, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "tmpl-uid", Generation: 2}}
	cl := fake.NewClientBuilder().WithScheme(scrapeScheme(t)).
		WithObjects(tmpl).
		WithStatusSubresource(&gameplanev1alpha1.GameTemplate{}).
		Build()
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "demo"}, tmpl); err != nil {
		t.Fatal(err)
	}
	r := &ModuleReconciler{Client: cl, Scheme: scrapeScheme(t), Namespace: "gp-system"}
	if rec != nil {
		r.EventRecorder = rec
	}
	return r, tmpl
}

func getLocalesCM(t *testing.T, r *ModuleReconciler) (*corev1.ConfigMap, error) {
	t.Helper()
	cm := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{Namespace: "gp-system", Name: "demo-locales"}, cm)
	return cm, err
}

func TestApplyLocales_WritesOwnedConfigMapAndWarns(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	r, tmpl := newLocalesFixture(t, rec)
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"},"module.summary":{"defaultMessage":"{x}"}}`),
	}))

	cm, err := getLocalesCM(t, r)
	if err != nil {
		t.Fatalf("ConfigMap: %v", err)
	}
	got, err := pack.ParseEntries([]byte(cm.Data["fr.json"]))
	if err != nil || len(got) != 1 {
		t.Fatalf("fr.json = %q, %v", cm.Data["fr.json"], err)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != tmpl.UID || cm.OwnerReferences[0].Kind != "GameTemplate" {
		t.Errorf("ownerReferences = %+v", cm.OwnerReferences)
	}

	stored := &gameplanev1alpha1.GameTemplate{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "demo"}, stored); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(stored.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "InvalidEntries" || cond.ObservedGeneration != 2 {
		t.Fatalf("condition = %+v", cond)
	}

	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, "fr:module.summary") {
			t.Errorf("event = %q", ev)
		}
	default:
		t.Fatal("no Warning event recorded")
	}

	// A second apply with the same problem set does not repeat the event.
	r.applyLocales(context.Background(), stored, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"},"module.summary":{"defaultMessage":"{x}"}}`),
	}))
	select {
	case ev := <-rec.Events:
		t.Errorf("repeated event: %q", ev)
	default:
	}
}

func TestApplyLocales_AllValidSetsTrueWithoutEvent(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	r, tmpl := newLocalesFixture(t, rec)
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`),
	}))
	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", cond)
	}
	if len(rec.Events) != 0 {
		t.Error("unexpected event")
	}
}

func TestApplyLocales_RemovesConfigMapWhenNothingValid(t *testing.T) {
	r, tmpl := newLocalesFixture(t, nil)
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`),
	}))
	if _, err := getLocalesCM(t, r); err != nil {
		t.Fatalf("ConfigMap should exist: %v", err)
	}
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"unknown.id":{"defaultMessage":"x"}}`),
	}))
	if _, err := getLocalesCM(t, r); !apierrors.IsNotFound(err) {
		t.Fatalf("ConfigMap should be gone, err = %v", err)
	}
}

func TestApplyLocales_NoLayersIsNoOpAndClearsStaleState(t *testing.T) {
	r, tmpl := newLocalesFixture(t, nil)
	// Nothing to do on a bundle without locales.
	r.applyLocales(context.Background(), tmpl, localesBundle(nil))
	if _, err := getLocalesCM(t, r); !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected ConfigMap, err = %v", err)
	}
	if len(tmpl.Status.Conditions) != 0 {
		t.Fatalf("unexpected conditions %v", tmpl.Status.Conditions)
	}

	// An older version shipped translations; the new one ships none.
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`),
	}))
	r.applyLocales(context.Background(), tmpl, localesBundle(nil))
	if _, err := getLocalesCM(t, r); !apierrors.IsNotFound(err) {
		t.Fatalf("stale ConfigMap remained, err = %v", err)
	}
	if c := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid); c != nil {
		t.Fatalf("stale condition remained: %+v", c)
	}
}

func TestApplyLocales_TooLargeWarnsAboutDroppedLanguages(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	r, tmpl := newLocalesFixture(t, rec)
	big := strings.Repeat("x", 500*1024)
	layer := []byte(fmt.Sprintf(`{"template.displayName":{"defaultMessage":%q}}`, big))
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{"de": layer, "fr": layer}))

	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Reason != "LocalesTooLarge" {
		t.Fatalf("condition = %+v", cond)
	}
	cm, err := getLocalesCM(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cm.Data["de.json"]; !ok || len(cm.Data) != 1 {
		t.Errorf("ConfigMap keys = %v; want only de.json", cm.Data)
	}
	if ev := <-rec.Events; !strings.Contains(ev, "LocalesTooLarge") || !strings.Contains(ev, "fr") {
		t.Errorf("event = %q", ev)
	}
}

func TestApplyLocales_NilRecorderAndNilInputsAreSafe(t *testing.T) {
	r, tmpl := newLocalesFixture(t, nil)
	r.applyLocales(context.Background(), nil, localesBundle(nil))
	r.applyLocales(context.Background(), tmpl, nil)
	r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"unknown.id":{"defaultMessage":"x"}}`),
	}))
}

func TestApplyLocales_BadTemplateYAMLIsLoggedNotFatal(t *testing.T) {
	r, tmpl := newLocalesFixture(t, nil)
	b := localesBundle(map[string][]byte{"fr": []byte(`{}`)})
	b.TemplateYAML = []byte("spec: [broken")
	r.applyLocales(context.Background(), tmpl, b)
	if _, err := getLocalesCM(t, r); !apierrors.IsNotFound(err) {
		t.Fatalf("ConfigMap written for an unparsable template, err = %v", err)
	}
}

func TestLocalesNamespaceDefault(t *testing.T) {
	if got := (&ModuleReconciler{}).localesNamespace(); got != "gameplane-system" {
		t.Errorf("default namespace = %q", got)
	}
	if got := (&ModuleReconciler{Namespace: "x"}).localesNamespace(); got != "x" {
		t.Errorf("namespace = %q", got)
	}
}

func drainEvents(rec *events.FakeRecorder) []string {
	var out []string
	for {
		select {
		case ev := <-rec.Events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestApplyLocales_OversizedLayerIsReportedNotDropped(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	r, tmpl := newLocalesFixture(t, rec)
	b := localesBundle(map[string][]byte{"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`)})
	b.OversizedLocales = []string{"de"}
	if err := r.applyLocales(context.Background(), tmpl, b); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "LocalesTooLarge" || !strings.Contains(cond.Message, "de") {
		t.Fatalf("condition = %+v", cond)
	}
	cm, err := getLocalesCM(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cm.Data["fr.json"]; !ok || len(cm.Data) != 1 {
		t.Errorf("ConfigMap keys = %v; want fr.json only", cm.Data)
	}
	evs := drainEvents(rec)
	found := false
	for _, ev := range evs {
		if strings.Contains(ev, "Warning") && strings.Contains(ev, "LocalesTooLarge") && strings.Contains(ev, "de") {
			found = true
		}
	}
	if !found {
		t.Errorf("events = %q; want a LocalesTooLarge warning naming de", evs)
	}
}

func TestApplyLocales_OnlyOversizedKeepsConditionAndNoConfigMap(t *testing.T) {
	r, tmpl := newLocalesFixture(t, nil)
	b := localesBundle(nil)
	b.OversizedLocales = []string{"de"}
	if err := r.applyLocales(context.Background(), tmpl, b); err != nil {
		t.Fatal(err)
	}
	if _, err := getLocalesCM(t, r); !apierrors.IsNotFound(err) {
		t.Fatalf("ConfigMap should be absent, err = %v", err)
	}
	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "LocalesTooLarge" {
		t.Fatalf("condition = %+v; want False/LocalesTooLarge", cond)
	}
}

func TestApplyLocales_WriteFailureIsReported(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "tmpl-uid", Generation: 2}}
	boom := errors.New("boom")
	cl := fake.NewClientBuilder().WithScheme(scrapeScheme(t)).
		WithObjects(tmpl).
		WithStatusSubresource(&gameplanev1alpha1.GameTemplate{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok {
					return boom
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "demo"}, tmpl); err != nil {
		t.Fatal(err)
	}
	r := &ModuleReconciler{Client: cl, Scheme: scrapeScheme(t), Namespace: "gp-system", EventRecorder: rec}

	err := r.applyLocales(context.Background(), tmpl, localesBundle(map[string][]byte{
		"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`),
	}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the write error", err)
	}
	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "WriteFailed" {
		t.Fatalf("condition = %+v; want False/WriteFailed", cond)
	}
	found := false
	for _, ev := range drainEvents(rec) {
		if strings.Contains(ev, "LocalesWriteFailed") {
			found = true
		}
	}
	if !found {
		t.Error("no LocalesWriteFailed event")
	}
}

func TestLocalesCondition_ReasonPriority(t *testing.T) {
	cases := []struct {
		name   string
		out    localeOutcome
		status metav1.ConditionStatus
		reason string
	}{
		{"all three", localeOutcome{WriteErr: errors.New("x"), Dropped: []string{"fr"}, PerLang: map[string]int{"de": 1}}, metav1.ConditionFalse, "WriteFailed"},
		{"too large and invalid", localeOutcome{Oversized: []string{"fr"}, PerLang: map[string]int{"de": 1}}, metav1.ConditionFalse, "LocalesTooLarge"},
		{"invalid", localeOutcome{PerLang: map[string]int{"de": 1}}, metav1.ConditionFalse, "InvalidEntries"},
		{"none", localeOutcome{}, metav1.ConditionTrue, "Valid"},
	}
	for _, c := range cases {
		got := localesCondition(c.out)
		if got.Status != c.status || got.Reason != c.reason {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, got.Status, got.Reason, c.status, c.reason)
		}
	}
	if got := localesCondition(localeOutcome{}); got.Message != "All module translations are valid." {
		t.Errorf("message = %q", got.Message)
	}
	if got := localesCondition(localeOutcome{Oversized: []string{"fr", "de"}}); !strings.Contains(got.Message, "fr, de") {
		t.Errorf("message = %q", got.Message)
	}
}

func TestApplyLocales_EventsFireOnlyWhenProblemListChanges(t *testing.T) {
	rec := events.NewFakeRecorder(16)
	r, tmpl := newLocalesFixture(t, rec)
	bad := func(id string) *modsrc.Bundle {
		return localesBundle(map[string][]byte{"fr": []byte(fmt.Sprintf(`{"template.displayName":{"defaultMessage":"Jeu"},%q:{"defaultMessage":"x"}}`, id))})
	}
	apply := func(b *modsrc.Bundle) {
		t.Helper()
		if err := r.applyLocales(context.Background(), tmpl, b); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int { return len(drainEvents(rec)) }

	apply(bad("unknown.a"))
	apply(bad("unknown.a"))
	if n := count(); n != 1 {
		t.Fatalf("same problems twice: %d events, want 1", n)
	}
	// Same counts, different ids: the list changed.
	apply(bad("unknown.b"))
	if n := count(); n != 1 {
		t.Fatalf("changed ids with identical counts: %d events, want 1", n)
	}
	// Problems, then none, then the same problems again: the list changed twice.
	apply(localesBundle(map[string][]byte{"fr": []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`)}))
	if tmpl.Annotations[localesProblemsAnnotation] != "" {
		t.Errorf("annotation = %q, want removed", tmpl.Annotations[localesProblemsAnnotation])
	}
	if n := count(); n != 0 {
		t.Fatalf("valid bundle: %d events, want 0", n)
	}
	apply(bad("unknown.b"))
	if n := count(); n != 1 {
		t.Fatalf("problems after a clean pull: %d events, want 1", n)
	}
}
