package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCurseforgeVersionsFillLoadersFromTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":1,"displayName":"nf","fileName":"nf.zip","downloadUrl":"https://edge.forgecdn.net/nf.zip","fileLength":1,"gameVersions":["1.21.1","NeoForge"],"releaseType":1,"fileDate":"2026-10-28T14:03:11Z"},
			{"id":2,"displayName":"multi","fileName":"m.zip","downloadUrl":"https://edge.forgecdn.net/m.zip","fileLength":1,"gameVersions":["Fabric","1.20.1","Quilt","fabric"],"releaseType":1,"fileDate":"2026-10-28T14:03:11Z"},
			{"id":3,"displayName":"none","fileName":"n.zip","downloadUrl":"https://edge.forgecdn.net/n.zip","fileLength":1,"gameVersions":["1.12.2","Java 8"],"releaseType":1,"fileDate":"2026-10-28T14:03:11Z"}
		]}`))
	}))
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", Filter{ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantLoaders := [][]string{{"neoforge"}, {"fabric", "quilt"}, nil}
	for i, want := range wantLoaders {
		if !reflect.DeepEqual(got[i].Loaders, want) {
			t.Errorf("version %d Loaders = %v, want %v", i, got[i].Loaders, want)
		}
	}
	// GameVersions stay as CurseForge lists them, loader tags included.
	if want := []string{"1.21.1", "NeoForge"}; !reflect.DeepEqual(got[0].GameVersions, want) {
		t.Errorf("GameVersions = %v, want %v", got[0].GameVersions, want)
	}
}

func TestCfLoaders(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want []string
	}{
		{"all four loaders in tag order", []string{"Quilt", "NEOFORGE", "forge", "Fabric"}, []string{"quilt", "neoforge", "forge", "fabric"}},
		{"game versions only", []string{"1.21.1", "Client", "Server"}, nil},
		{"repeated loader collapses", []string{"Forge", "forge"}, []string{"forge"}},
		{"empty", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfLoaders(tc.tags); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("cfLoaders(%v) = %v, want %v", tc.tags, got, tc.want)
			}
		})
	}
}
