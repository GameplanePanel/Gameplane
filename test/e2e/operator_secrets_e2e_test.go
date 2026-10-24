//go:build e2e

package e2e

import (
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHelmInstall_OperatorSecretNamespaceBoundary(t *testing.T) {
	t.Parallel()
	for _, namespace := range []string{"gameplane-games", "gameplane-system", "kube-system", ""} {
		for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete"} {
			want := namespace == "gameplane-games" || namespace == "gameplane-system" && (verb == "get" || verb == "list" || verb == "watch")
			review, err := envInstance.K8s.AuthorizationV1().SubjectAccessReviews().Create(t.Context(), &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User:               "system:serviceaccount:gameplane-system:gameplane-operator",
					Groups:             []string{"system:serviceaccounts", "system:serviceaccounts:gameplane-system", "system:authenticated"},
					ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: namespace, Verb: verb, Resource: "secrets"},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if review.Status.Allowed != want || review.Status.EvaluationError != "" {
				t.Errorf("operator %s Secrets in %q: allowed=%t, want=%t, reason=%s, error=%s", verb, namespace, review.Status.Allowed, want, review.Status.Reason, review.Status.EvaluationError)
			}
		}
	}
}
