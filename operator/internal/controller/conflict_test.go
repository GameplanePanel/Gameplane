package controller

import (
	"errors"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestRequeueOnConflict(t *testing.T) {
	gr := schema.GroupResource{Group: "gameplane.local", Resource: "backups"}
	conflict := apierrors.NewConflict(gr, "b", errors.New("the object has been modified"))
	other := errors.New("boom")
	keep := ctrl.Result{RequeueAfter: 5 * time.Second}

	cases := []struct {
		name    string
		res     ctrl.Result
		err     error
		wantRes ctrl.Result
		wantErr bool
	}{
		{name: "conflict becomes quiet paced retry", res: keep, err: conflict, wantRes: ctrl.Result{RequeueAfter: time.Second}},
		{name: "wrapped conflict becomes quiet paced retry", res: keep, err: fmt.Errorf("update status: %w", conflict), wantRes: ctrl.Result{RequeueAfter: time.Second}},
		{name: "other error passes through", res: keep, err: other, wantRes: keep, wantErr: true},
		{name: "nil error passes result through", res: keep, err: nil, wantRes: keep},
		{name: "settled reconcile stays settled", res: ctrl.Result{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requeueOnConflict(tc.res, tc.err)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, tc.err) {
				t.Fatalf("non-conflict error changed: %v, want %v", err, tc.err)
			}
			if got != tc.wantRes {
				t.Errorf("result = %+v, want %+v", got, tc.wantRes)
			}
		})
	}
}
