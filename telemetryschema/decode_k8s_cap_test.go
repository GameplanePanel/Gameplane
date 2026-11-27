package telemetryschema

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// k8sArray renders the JSON array of the given values.
func k8sArray(values ...string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// minorVersions returns 1.0 through 1.(n-1) as distinct values.
func minorVersions(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("1.%d", i))
	}
	return out
}

// TestDecodeExtSchema2K8sCap checks the MaxK8sVersions cap on schema 2
// env.k8s: a set at the cap decodes, one distinct value over it is rejected
// with ErrInvalidPayload, and repeated values do not count toward the cap.
func TestDecodeExtSchema2K8sCap(t *testing.T) {
	t.Parallel()
	atCap := minorVersions(MaxK8sVersions)
	overCap := minorVersions(MaxK8sVersions + 1)

	cases := []struct {
		name    string
		k8s     string
		wantErr bool
		wantLen int
	}{
		{"at the cap is accepted", k8sArray(atCap...), false, MaxK8sVersions},
		{"one over the cap is rejected", k8sArray(overCap...), true, 0},
		{"repeats do not count toward the cap", k8sArray(append(append([]string{}, atCap...), atCap...)...), false, MaxK8sVersions},
		{"out-of-set values fold to one value", k8sArray(append(minorVersions(MaxK8sVersions-1), "v1.1", "v1.2")...), false, MaxK8sVersions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := mut2(`["1.31","1.30","1.31"]`, tc.k8s)
			r, _, err := Decode(withExt(body))
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidPayload) {
					t.Fatalf("Decode error = %v, want ErrInvalidPayload", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if r.Ext == nil {
				t.Fatal("Ext is nil")
			}
			if got := len(r.Ext.Env.K8s); got != tc.wantLen {
				t.Fatalf("len(K8s) = %d, want %d", got, tc.wantLen)
			}
		})
	}
}
