package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestApplyLocales_StaleDeleteFailureIsWriteFailed(t *testing.T) {
	rec := events.NewFakeRecorder(8)
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "tmpl-uid", Generation: 2}}
	boom := errors.New("boom")
	cl := fake.NewClientBuilder().WithScheme(scrapeScheme(t)).
		WithObjects(tmpl).
		WithStatusSubresource(&gameplanev1alpha1.GameTemplate{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok {
					return boom
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).Build()
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "demo"}, tmpl); err != nil {
		t.Fatal(err)
	}
	r := &ModuleReconciler{Client: cl, Scheme: scrapeScheme(t), Namespace: "gp-system", EventRecorder: rec}

	if err := r.applyLocales(context.Background(), tmpl, localesBundle(nil)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the delete error", err)
	}
	cond := meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "WriteFailed" {
		t.Fatalf("condition = %+v; want False/WriteFailed", cond)
	}
}

func TestProblemsFingerprint_WriteErrorTextIsIgnored(t *testing.T) {
	a := problemsFingerprint(localeOutcome{WriteErr: errors.New("timeout after 1.2s")})
	b := problemsFingerprint(localeOutcome{WriteErr: errors.New("timeout after 3.4s")})
	if a == "" || a != b {
		t.Fatalf("fingerprints %q and %q; want equal and non-empty", a, b)
	}
}

func TestBuildLocales_OversizedInvalidTagIsRejected(t *testing.T) {
	out := buildLocalesWithOversized(localesRef(), nil, []string{"de", "not a tag"})
	if len(out.Oversized) != 1 || out.Oversized[0] != "de" {
		t.Fatalf("Oversized = %v; want [de]", out.Oversized)
	}
	if len(out.Rejected) != 1 || out.Rejected[0] != "not a tag" {
		t.Fatalf("Rejected = %v", out.Rejected)
	}
}

func TestLocalesCondition_MessageIsBounded(t *testing.T) {
	tags := make([]string, 0, 5000)
	for i := 0; i < 5000; i++ {
		tags = append(tags, strings.Repeat("x", 8))
	}
	c := localesCondition(localeOutcome{Oversized: tags})
	if len(c.Message) > maxConditionMessageBytes {
		t.Fatalf("message length %d exceeds %d", len(c.Message), maxConditionMessageBytes)
	}
}
