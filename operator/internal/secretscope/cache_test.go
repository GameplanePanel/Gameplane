package secretscope

import (
	"reflect"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestCacheOptionsScopesOnlySecretsToExplicitUnion(t *testing.T) {
	opts, err := CacheOptions("games-a,games-b,games-a", "control", "ca", "modules")
	if err != nil {
		t.Fatal(err)
	}
	if opts.DefaultNamespaces != nil || len(opts.ByObject) != 1 {
		t.Fatal("non-Secret resources must retain their existing cluster-wide cache")
	}
	var namespaces []string
	for object, objectOpts := range opts.ByObject {
		if _, ok := object.(*corev1.Secret); !ok {
			t.Fatalf("unexpected scoped resource %T", object)
		}
		for namespace := range objectOpts.Namespaces {
			namespaces = append(namespaces, namespace)
		}
	}
	sort.Strings(namespaces)
	if want := []string{"ca", "control", "games-a", "games-b", "modules"}; !reflect.DeepEqual(namespaces, want) {
		t.Fatalf("Secret namespaces = %v, want %v", namespaces, want)
	}
}

func TestCacheOptionsRejectsUnboundedOrMalformedNamespaces(t *testing.T) {
	for _, games := range []string{"", "*", ",", "games,", ",games", "games,,other", " games", "Games", "games/other"} {
		t.Run(games, func(t *testing.T) {
			if _, err := CacheOptions(games, "control", "ca", "modules"); err == nil {
				t.Fatalf("accepted invalid game namespaces %q", games)
			}
		})
	}
	for _, auxiliary := range [][3]string{{"", "ca", "modules"}, {"control", "", "modules"}, {"control", "ca", ""}, {"*", "ca", "modules"}} {
		if _, err := CacheOptions("games", auxiliary[0], auxiliary[1], auxiliary[2]); err == nil {
			t.Fatalf("accepted invalid auxiliary namespaces %v", auxiliary)
		}
	}
}
