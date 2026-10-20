package telemetryschema

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// validExt2 is validExt on schema 2: k8s and distro are sets and the
// deployment is set.
var validExt2 = strings.Replace(strings.Replace(validExt, `"schema":1,`, `"schema":2,`, 1),
	`"env":{"k8s":"1.31","distro":"k3s","arch":["amd64"],"nodes":"1"}`,
	`"env":{"k8s":["1.31","1.30","1.31"],"distro":["k3s","eks"],"arch":["amd64"],"nodes":"4-10","deployment":"standalone"}`, 1)

// mut2 replaces old with replacement in validExt2 and panics when old is
// absent, so a stale test fixture fails loudly instead of silently testing
// nothing.
func mut2(old, replacement string) string {
	if !strings.Contains(validExt2, old) {
		panic("schema 2 fixture does not contain " + old)
	}
	return strings.Replace(validExt2, old, replacement, 1)
}

// TestDecodeExtSchema2Valid decodes the schema 2 fixture: the sets come back
// sorted and de-duplicated, and the deployment is kept.
func TestDecodeExtSchema2Valid(t *testing.T) {
	t.Parallel()
	r, info, err := Decode(withExt(validExt2))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if info.ExtDropped {
		t.Fatal("ExtDropped = true for a valid schema 2 ext")
	}
	if r.Ext == nil {
		t.Fatal("Ext is nil")
	}
	if r.Ext.Schema != 2 {
		t.Fatalf("Schema = %d, want 2", r.Ext.Schema)
	}
	want := Env{K8s: []string{"1.30", "1.31"}, Distro: []string{"eks", "k3s"}, Arch: []string{"amd64"}, Nodes: "4-10", Deployment: "standalone"}
	if !reflect.DeepEqual(r.Ext.Env, want) {
		t.Fatalf("Env = %+v, want %+v", r.Ext.Env, want)
	}
}

// TestDecodeExtSchema2Folds checks that out-of-set values in the schema 2
// sets and deployment fold to "other", and that known values are kept.
func TestDecodeExtSchema2Folds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ext  string
		got  func(Env) any
		want any
	}{
		{"k8s folds out-of-set members", mut2(`["1.31","1.30","1.31"]`, `["v1.31","1.29"]`), func(e Env) any { return e.K8s }, []string{"1.29", "other"}},
		{"distro folds unknown", mut2(`["k3s","eks"]`, `["mystery"]`), func(e Env) any { return e.Distro }, []string{"other"}},
		{"deployment folds unknown", mut2(`"deployment":"standalone"`, `"deployment":"edge"`), func(e Env) any { return e.Deployment }, "other"},
		{"deployment cluster kept", mut2(`"deployment":"standalone"`, `"deployment":"cluster"`), func(e Env) any { return e.Deployment }, "cluster"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, _, err := Decode(withExt(tc.ext))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got := tc.got(r.Ext.Env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestDecodeExtSchema2Rejects covers the schema 2 env rules: deployment is
// required, the sets are non-empty arrays of strings, and no unknown keys are
// allowed. None of these is an unsupported-schema error.
func TestDecodeExtSchema2Rejects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"missing deployment": mut2(`,"deployment":"standalone"`, ``),
		"empty k8s":          mut2(`["1.31","1.30","1.31"]`, `[]`),
		"empty distro":       mut2(`["k3s","eks"]`, `[]`),
		"k8s as a string":    mut2(`["1.31","1.30","1.31"]`, `"1.31"`),
		"distro as a string": mut2(`["k3s","eks"]`, `"k3s"`),
		"k8s null member":    mut2(`["1.31","1.30","1.31"]`, `[null]`),
		"deployment number":  mut2(`"deployment":"standalone"`, `"deployment":1`),
		"unknown env key":    mut2(`"nodes":"4-10"`, `"nodes":"4-10","extra":1`),
	}
	for name, ext := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, info, err := Decode(withExt(ext))
			if err == nil {
				t.Fatalf("expected error, got %+v (info %+v)", got, info)
			}
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error chain missing ErrInvalidPayload: %v", err)
			}
			if errors.Is(err, ErrUnsupportedSchema) {
				t.Fatalf("error must not be ErrUnsupportedSchema: %v", err)
			}
		})
	}
}

// TestDecodeExtSchema1RefusesDeployment checks that a schema 1 env may not
// carry a deployment: the field belongs to schema 2.
func TestDecodeExtSchema1RefusesDeployment(t *testing.T) {
	t.Parallel()
	ext := mut(`"nodes":"1"}`, `"nodes":"1","deployment":"cluster"}`)
	_, _, err := Decode(withExt(ext))
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err = %v, want ErrInvalidPayload", err)
	}
}

// TestDecodeExtSchema1DecodesAsOneValueSets checks that a schema 1 report
// decodes k8s and distro as one-value sets and its deployment as "other"
// (OD-14).
func TestDecodeExtSchema1DecodesAsOneValueSets(t *testing.T) {
	t.Parallel()
	r, _, err := Decode(withExt(validExt))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if r.Ext == nil {
		t.Fatal("Ext is nil")
	}
	want := Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64"}, Nodes: "1", Deployment: "other"}
	if !reflect.DeepEqual(r.Ext.Env, want) {
		t.Fatalf("Env = %+v, want %+v", r.Ext.Env, want)
	}
}

// TestEncodeSchema2RoundTrip checks that a schema 2 report encodes its sets
// sorted and that Decode returns the same environment.
func TestEncodeSchema2RoundTrip(t *testing.T) {
	t.Parallel()
	r := Report{
		Version: "0.3.0", Servers: 3, Templates: 7,
		Ext: &Extended{
			Schema:    2,
			InstallID: testID,
			Env: Env{
				K8s: []string{"1.31", "1.30"}, Distro: []string{"k3s", "eks"}, Arch: []string{"amd64"},
				Nodes: "1", Deployment: "cluster",
			},
			Games:    Games{Official: map[string]int{"minecraft-java": 2}, Custom: 1},
			Features: Features{Tunnels: []string{"playit"}, Clusters: "1", DB: "sqlite", Language: "en"},
			Key:      testKey,
			SentAt:   time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC),
		},
	}
	body, err := Encode(r)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	const frag = `"env":{"k8s":["1.30","1.31"],"distro":["eks","k3s"],"arch":["amd64"],"nodes":"1","deployment":"cluster"}`
	if !strings.Contains(string(body), frag) {
		t.Fatalf("body %s does not contain %s", body, frag)
	}
	back, info, err := Decode(body)
	if err != nil {
		t.Fatalf("Decode(Encode(r)): %v", err)
	}
	if info.ExtDropped {
		t.Fatal("ExtDropped = true for an encoded schema 2 report")
	}
	want := Env{K8s: []string{"1.30", "1.31"}, Distro: []string{"eks", "k3s"}, Arch: []string{"amd64"}, Nodes: "1", Deployment: "cluster"}
	if !reflect.DeepEqual(back.Ext.Env, want) {
		t.Fatalf("Env = %+v, want %+v", back.Ext.Env, want)
	}
}

// TestEncodeSchema1WritesSingleValues checks that a schema 1 report writes
// k8s and distro as single values and no deployment, folding a multi-value
// set to "other".
func TestEncodeSchema1WritesSingleValues(t *testing.T) {
	t.Parallel()
	r := Report{Ext: &Extended{
		Schema:    1,
		InstallID: testID,
		Env: Env{
			K8s: []string{"1.31", "1.30"}, Distro: []string{"k3s"}, Arch: []string{"amd64"},
			Nodes: "1", Deployment: "cluster",
		},
	}}
	body, err := Encode(r)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	const frag = `"env":{"k8s":"other","distro":"k3s","arch":["amd64"],"nodes":"1"}`
	if !strings.Contains(string(body), frag) {
		t.Fatalf("body %s does not contain %s", body, frag)
	}
	if strings.Contains(string(body), "deployment") {
		t.Fatalf("schema 1 body must not carry a deployment: %s", body)
	}
}
