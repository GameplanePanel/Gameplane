package registry

import (
	"context"
	"testing"
)

func TestFTBVersionsCarryReleaseTypeAndPublishedAt(t *testing.T) {
	pack := []byte(`{"id":130,"name":"meta","versions":[
		{"id":1,"name":"1.0","type":"release","updated":1700000001,"targets":[{"name":"neoforge","type":"modloader","version":"21.1"},{"name":"1.21.1","type":"game","version":"1.21.1"}]},
		{"id":2,"name":"0.9","type":"beta","updated":0,"targets":[{"name":"neoforge","type":"modloader","version":"21.1"},{"name":"1.21.1","type":"game","version":"1.21.1"}]},
		{"id":3,"name":"old","type":"archived","updated":1700000003,"targets":[{"name":"neoforge","type":"modloader","version":"21.1"},{"name":"1.21.1","type":"game","version":"1.21.1"}]}
	]}`)
	srv := newFTBServer(t, nil, func(int) ([]byte, int) { return pack, 0 })

	got, err := testFTB(srv.URL).Versions(context.Background(), "130", Filter{Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (archived dropped)", len(got))
	}
	if got[0].ID != "1" || got[0].ReleaseType != "release" || got[0].PublishedAt != "2023-11-14T22:13:21Z" {
		t.Errorf("release version = %+v", got[0])
	}
	if got[1].ID != "2" || got[1].ReleaseType != "beta" || got[1].PublishedAt != "" {
		t.Errorf("beta version without updated = %+v", got[1])
	}
}
