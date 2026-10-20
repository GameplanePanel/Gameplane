package oci

import (
	"context"
	"strings"
	"testing"
)

func TestLocaleBudget_Take(t *testing.T) {
	var b LocaleBudget
	if !b.Take(MaxLocaleTotalSize - 10) {
		t.Fatal("first take within the budget was refused")
	}
	if b.Take(11) {
		t.Fatal("take over the budget was accepted")
	}
	if !b.Take(10) {
		t.Fatal("a refused take must reserve nothing, so a smaller one still fits")
	}
	if b.Take(1) {
		t.Fatal("budget is full; take was accepted")
	}
	var neg LocaleBudget
	if neg.Take(-1) {
		t.Fatal("negative size was accepted")
	}
}

func TestClient_Pull_LocaleTotalBudget(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/locale-budget"
	// 800 KiB each: five fit in the 4 MiB budget, the sixth does not.
	body := []byte(strings.Repeat("x", 800*1024))
	layers := map[string]typedLayer{
		LayerNameMetadata: {MediaTypeMetadata, []byte(fixtureModuleYAML)},
		LayerNameTemplate: {MediaTypeTemplate, []byte(fixtureTemplateYAML)},
	}
	for _, tag := range []string{"aa", "bb", "cc", "dd", "ee", "ff"} {
		layers["locales/"+tag+".json"] = typedLayer{MediaTypeLocale, body}
	}
	reg.pushTyped(repo, "1.0.0", layers)

	c := New(nil, true)
	_, files, err := c.Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	locales, oversized := SplitLocaleLayers(files)
	if len(locales) != 5 {
		t.Errorf("read %d locale files, want 5", len(locales))
	}
	if len(oversized) != 1 || oversized[0] != "ff" {
		t.Errorf("oversized = %v, want [ff]", oversized)
	}
}
