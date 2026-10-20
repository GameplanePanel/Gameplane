package telemetryschema

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testID  = "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10"
	testKey = "sVjEWoD28OPh_Gx5V4QWBw8kMurJ9uZS_r1JDe8Dgls"

	basicBody = `{"version":"1.0.0","servers":1,"templates":2}`

	// validExt is a complete, valid ext object. Tests derive variants from it
	// with mut, setAt and delAt.
	validExt = `{"schema":1,"installId":"` + testID + `",` +
		`"env":{"k8s":"1.31","distro":"k3s","arch":["amd64"],"nodes":"1"},` +
		`"games":{"official":{"minecraft-java":2,"terraria":1},"custom":0},` +
		`"features":{"wakeOnConnect":true,"tunnels":["playit"],"capture":false,"backups":true,` +
		`"sso":false,"auditForwarding":false,"clusters":"1","db":"sqlite","language":"en"},` +
		`"key":"` + testKey + `","sentAt":"2026-10-06T09:12:44Z"}`
)

// withExt wraps an ext object in a complete report body.
func withExt(ext string) []byte {
	return []byte(`{"version":"0.3.0","servers":3,"templates":7,"ext":` + ext + `}`)
}

// mut replaces old with replacement in validExt and panics when old is absent,
// so a stale test fixture fails loudly instead of silently testing nothing.
func mut(old, replacement string) string {
	if !strings.Contains(validExt, old) {
		panic("fixture does not contain " + old)
	}
	return strings.Replace(validExt, old, replacement, 1)
}

// edit parses validExt into a generic tree, applies fn to the object at path
// (every element but the last is a nested object) and re-encodes it.
func edit(t *testing.T, path []string, fn func(obj map[string]any, key string)) string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(validExt), &root); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	obj := root
	for _, p := range path[:len(path)-1] {
		next, ok := obj[p].(map[string]any)
		if !ok {
			t.Fatalf("fixture path %v: %q is not an object", path, p)
		}
		obj = next
	}
	fn(obj, path[len(path)-1])
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("encode variant: %v", err)
	}
	return string(out)
}

// setAt returns validExt with the value at path replaced by val.
func setAt(t *testing.T, val any, path ...string) string {
	t.Helper()
	return edit(t, path, func(obj map[string]any, key string) {
		if _, ok := obj[key]; !ok {
			t.Fatalf("fixture has no key %v", path)
		}
		obj[key] = val
	})
}

// delAt returns validExt with the key at path removed.
func delAt(t *testing.T, path ...string) string {
	t.Helper()
	return edit(t, path, func(obj map[string]any, key string) {
		if _, ok := obj[key]; !ok {
			t.Fatalf("fixture has no key %v", path)
		}
		delete(obj, key)
	})
}

// TestDecodeBasicAccepts covers valid basic reports, including zero counts and
// trailing whitespace.
func TestDecodeBasicAccepts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want Report
	}{
		{"minimal", basicBody, Report{Version: "1.0.0", Servers: 1, Templates: 2}},
		{"zero counts", `{"version":"1.0.0","servers":0,"templates":0}`, Report{Version: "1.0.0"}},
		{"trailing newline", basicBody + "\n", Report{Version: "1.0.0", Servers: 1, Templates: 2}},
		{"surrounding whitespace", "  " + basicBody + "  \r\n\t", Report{Version: "1.0.0", Servers: 1, Templates: 2}},
		{"any key order", `{"templates":2,"servers":1,"version":"1.0.0"}`, Report{Version: "1.0.0", Servers: 1, Templates: 2}},
		{"hostile version is kept for the receiver to bucket", `{"version":"<script>","servers":0,"templates":0}`, Report{Version: "<script>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, info, err := Decode([]byte(tc.body))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if info.ExtDropped {
				t.Fatal("ExtDropped = true for a basic report")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDecodeBasicRejects ports every reject case of the receiver's
// TestDecodePayloadRequiresExactKeys and TestIngestRequiresCompleteSingleReport
// and adds negative counts.
func TestDecodeBasicRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"capitalized key Servers", `{"version":"1.0.0","Servers":1,"templates":2}`},
		{"all-caps VERSION", `{"VERSION":"1.0.0","servers":1,"templates":2}`},
		{"duplicate servers", `{"version":"1.0.0","servers":1,"servers":2,"templates":3}`},
		{"duplicate version", `{"version":"1.0.0","version":"2.0.0","servers":1,"templates":2}`},
		{"null version", `{"version":null,"servers":1,"templates":2}`},
		{"null servers", `{"version":"1.0.0","servers":null,"templates":2}`},
		{"null templates", `{"version":"1.0.0","servers":1,"templates":null}`},
		{"servers as string", `{"version":"1.0.0","servers":"1","templates":2}`},
		{"templates as string", `{"version":"1.0.0","servers":1,"templates":"2"}`},
		{"version as integer", `{"version":1,"servers":1,"templates":2}`},
		{"servers as float", `{"version":"1.0.0","servers":1.5,"templates":2}`},
		{"missing version", `{"servers":1,"templates":2}`},
		{"missing servers", `{"version":"1.0.0","templates":2}`},
		{"missing templates", `{"version":"1.0.0","servers":1}`},
		{"empty object", `{}`},
		{"unknown field hostname", `{"version":"1.0.0","servers":1,"templates":2,"hostname":"prod"}`},
		{"unknown field at start", `{"unknown":"field","version":"1.0.0","servers":1,"templates":2}`},
		{"negative servers", `{"version":"1.0.0","servers":-1,"templates":0}`},
		{"negative templates", `{"version":"1.0.0","servers":0,"templates":-2}`},
		{"null body", `null`},
		{"array body", `[` + basicBody + `]`},
		{"number body", `42`},
		{"string body", `"x"`},
		{"empty body", ``},
		{"whitespace body", "  \n"},
		{"unterminated object", `{"version":"1.0.0","servers":1`},
		{"missing value", `{"version":"1.0.0","servers":}`},
		{"not json", `{not json`},
		{"non-string key", `{1:2}`},
		{"missing comma", `{"version":"1.0.0" "servers":1,"templates":2}`},
		{"trailing garbage", basicBody + `xyz`},
		{"second object", basicBody + basicBody},
		{"trailing value", basicBody + ` 1`},
		{"trailing empty object", basicBody + ` {}`},
		{"ext null", `{"version":"1.0.0","servers":1,"templates":2,"ext":null}`},
		{"ext empty object", `{"version":"1.0.0","servers":1,"templates":2,"ext":{}}`},
		{"ext as array", `{"version":"1.0.0","servers":1,"templates":2,"ext":[]}`},
		{"ext as string", `{"version":"1.0.0","servers":1,"templates":2,"ext":"x"}`},
		{"ext as number", `{"version":"1.0.0","servers":1,"templates":2,"ext":5}`},
		{"ext as bool", `{"version":"1.0.0","servers":1,"templates":2,"ext":true}`},
		{"duplicate ext", `{"version":"1.0.0","servers":1,"templates":2,"ext":` + validExt + `,"ext":` + validExt + `}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, info, err := Decode([]byte(tc.body))
			if err == nil {
				t.Fatalf("expected error, got report %+v", got)
			}
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error chain missing ErrInvalidPayload: %v", err)
			}
			if errors.Is(err, ErrUnsupportedSchema) {
				t.Fatalf("error must not be ErrUnsupportedSchema: %v", err)
			}
			if !reflect.DeepEqual(got, Report{}) || info != (DecodeInfo{}) {
				t.Fatalf("an error must return zero values, got %+v, %+v", got, info)
			}
		})
	}
}

// TestDecodeKeepsTrailingDecodeError verifies that trailing malformed JSON
// after a valid report is detected and that the root error is preserved.
func TestDecodeKeepsTrailingDecodeError(t *testing.T) {
	t.Parallel()
	_, _, err := Decode([]byte(`{"version":"v","servers":1,"templates":1} x`))
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("error chain missing ErrInvalidPayload: %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("error chain missing json.SyntaxError: %v", err)
	}
}

// TestDecodeExtValid decodes the full example from the contract.
func TestDecodeExtValid(t *testing.T) {
	t.Parallel()
	got, info, err := Decode(withExt(validExt))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if info.ExtDropped {
		t.Fatal("ExtDropped = true for a valid ext")
	}
	if got.Version != "0.3.0" || got.Servers != 3 || got.Templates != 7 {
		t.Fatalf("basic part = %+v", got)
	}
	want := &Extended{
		Schema:    1,
		InstallID: testID,
		Env:       Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64"}, Nodes: "1", Deployment: "other"},
		Games:     Games{Official: map[string]int{"minecraft-java": 2, "terraria": 1}, Custom: 0},
		Features: Features{
			WakeOnConnect: true, Tunnels: []string{"playit"}, Backups: true,
			Clusters: "1", DB: "sqlite", Language: "en",
		},
		Key:    testKey,
		SentAt: time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC),
	}
	if got.Ext == nil {
		t.Fatal("Ext is nil")
	}
	if !got.Ext.SentAt.Equal(want.SentAt) {
		t.Fatalf("SentAt = %v, want %v", got.Ext.SentAt, want.SentAt)
	}
	got.Ext.SentAt, want.SentAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got.Ext, want) {
		t.Fatalf("Ext = %+v, want %+v", got.Ext, want)
	}
}

// TestDecodeExtRejects covers every structural ext error: unknown, duplicate,
// null, missing and wrongly typed members at each nesting level, plus the
// range, key and time rules.
func TestDecodeExtRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		// unknown keys at every level, and case-sensitivity
		"unknown ext key":      mut(`"schema":1,`, `"schema":1,"extra":1,`),
		"unknown env key":      mut(`"nodes":"1"}`, `"nodes":"1","x":1}`),
		"unknown games key":    mut(`"custom":0}`, `"custom":0,"x":1}`),
		"unknown features key": mut(`"language":"en"}`, `"language":"en","x":1}`),
		"wrong-case ext key":   mut(`"installId"`, `"installid"`),
		"wrong-case feature":   mut(`"wakeOnConnect"`, `"wakeonconnect"`),
		// duplicate keys at every level
		"duplicate ext key":      mut(`"schema":1,`, `"schema":1,"schema":1,`),
		"duplicate env key":      mut(`"nodes":"1"}`, `"nodes":"1","nodes":"1"}`),
		"duplicate games key":    mut(`"custom":0}`, `"custom":0,"custom":0}`),
		"duplicate official key": mut(`"terraria":1}`, `"terraria":1,"terraria":2}`),
		"duplicate feature key":  mut(`"language":"en"}`, `"language":"en","language":"en"}`),
		// nulls at every level
		"null schema":       setAt(t, nil, "schema"),
		"null installId":    setAt(t, nil, "installId"),
		"null env":          setAt(t, nil, "env"),
		"null env.k8s":      setAt(t, nil, "env", "k8s"),
		"null env.arch":     setAt(t, nil, "env", "arch"),
		"null arch member":  mut(`["amd64"]`, `[null]`),
		"null games":        setAt(t, nil, "games"),
		"null official":     setAt(t, nil, "games", "official"),
		"null official val": mut(`"terraria":1`, `"terraria":null`),
		"null custom":       setAt(t, nil, "games", "custom"),
		"null features":     setAt(t, nil, "features"),
		"null feature bool": setAt(t, nil, "features", "sso"),
		"null tunnels":      setAt(t, nil, "features", "tunnels"),
		"null tunnel":       mut(`["playit"]`, `[null]`),
		"null key":          setAt(t, nil, "key"),
		"null sentAt":       setAt(t, nil, "sentAt"),
		// missing members: every ext field is required
		"missing schema":          delAt(t, "schema"),
		"missing installId":       delAt(t, "installId"),
		"missing env":             delAt(t, "env"),
		"missing env.k8s":         delAt(t, "env", "k8s"),
		"missing env.distro":      delAt(t, "env", "distro"),
		"missing env.arch":        delAt(t, "env", "arch"),
		"missing env.nodes":       delAt(t, "env", "nodes"),
		"missing games":           delAt(t, "games"),
		"missing games.official":  delAt(t, "games", "official"),
		"missing games.custom":    delAt(t, "games", "custom"),
		"missing features":        delAt(t, "features"),
		"missing wakeOnConnect":   delAt(t, "features", "wakeOnConnect"),
		"missing tunnels":         delAt(t, "features", "tunnels"),
		"missing capture":         delAt(t, "features", "capture"),
		"missing backups":         delAt(t, "features", "backups"),
		"missing sso":             delAt(t, "features", "sso"),
		"missing auditForwarding": delAt(t, "features", "auditForwarding"),
		"missing clusters":        delAt(t, "features", "clusters"),
		"missing db":              delAt(t, "features", "db"),
		"missing language":        delAt(t, "features", "language"),
		"missing key":             delAt(t, "key"),
		"missing sentAt":          delAt(t, "sentAt"),
		// wrong types
		"schema string":         setAt(t, "1", "schema"),
		"schema float":          setAt(t, 1.5, "schema"),
		"schema bool":           setAt(t, true, "schema"),
		"installId number":      setAt(t, 5, "installId"),
		"env number":            setAt(t, 5, "env"),
		"env array":             setAt(t, []any{}, "env"),
		"env string":            setAt(t, "x", "env"),
		"k8s number":            setAt(t, 1.31, "env", "k8s"),
		"distro number":         setAt(t, 1, "env", "distro"),
		"arch string":           setAt(t, "amd64", "env", "arch"),
		"arch number":           setAt(t, 5, "env", "arch"),
		"arch member number":    setAt(t, []any{1}, "env", "arch"),
		"nodes number":          setAt(t, 1, "env", "nodes"),
		"games number":          setAt(t, 5, "games"),
		"official array":        setAt(t, []any{}, "games", "official"),
		"official string":       setAt(t, "x", "games", "official"),
		"official count string": mut(`"terraria":1`, `"terraria":"1"`),
		"official count float":  mut(`"terraria":1`, `"terraria":1.5`),
		"official count bool":   mut(`"terraria":1`, `"terraria":true`),
		"custom string":         setAt(t, "0", "games", "custom"),
		"custom float":          setAt(t, 0.5, "games", "custom"),
		"features array":        setAt(t, []any{}, "features"),
		"wakeOnConnect string":  setAt(t, "true", "features", "wakeOnConnect"),
		"capture number":        setAt(t, 1, "features", "capture"),
		"backups string":        setAt(t, "x", "features", "backups"),
		"sso number":            setAt(t, 0, "features", "sso"),
		"auditForwarding str":   setAt(t, "false", "features", "auditForwarding"),
		"tunnels string":        setAt(t, "frp", "features", "tunnels"),
		"tunnel member number":  setAt(t, []any{1}, "features", "tunnels"),
		"clusters number":       setAt(t, 1, "features", "clusters"),
		"db number":             setAt(t, 1, "features", "db"),
		"language number":       setAt(t, 1, "features", "language"),
		"key number":            setAt(t, 5, "key"),
		"sentAt number":         setAt(t, 5, "sentAt"),
		// ranges
		"schema zero":               setAt(t, 0, "schema"),
		"schema negative":           setAt(t, -1, "schema"),
		"empty arch":                setAt(t, []any{}, "env", "arch"),
		"official count zero":       mut(`"terraria":1`, `"terraria":0`),
		"official count negative":   mut(`"terraria":1`, `"terraria":-3`),
		"unknown module count 0":    mut(`"terraria":1`, `"nope":0`),
		"custom negative":           setAt(t, -1, "games", "custom"),
		"custom total overflows":    mut(`"official":{"minecraft-java":2,"terraria":1},"custom":0`, `"official":{"a":1,"b":9223372036854775807},"custom":0`),
		"key empty":                 setAt(t, "", "key"),
		"key too short":             setAt(t, testKey[:42], "key"),
		"key too long":              setAt(t, testKey+"AA", "key"),
		"key padded":                setAt(t, testKey+"=", "key"),
		"key standard base64":       setAt(t, strings.ReplaceAll(testKey, "_", "/"), "key"),
		"key not base64":            setAt(t, "!!!"+testKey[3:], "key"),
		"key non-canonical":         setAt(t, testKey[:42]+"t", "key"),
		"key with newline":          setAt(t, testKey[:20]+"\n"+testKey[20:], "key"),
		"sentAt empty":              setAt(t, "", "sentAt"),
		"sentAt words":              setAt(t, "yesterday", "sentAt"),
		"sentAt date only":          setAt(t, "2026-10-06", "sentAt"),
		"sentAt space separator":    setAt(t, "2026-10-06 09:12:44Z", "sentAt"),
		"sentAt no zone":            setAt(t, "2026-10-06T09:12:44", "sentAt"),
		"sentAt month out of range": setAt(t, "2026-13-06T09:12:44Z", "sentAt"),
		"sentAt unix time":          setAt(t, "1791277964", "sentAt"),
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

// TestDecodeExtFoldsOutOfSetValues checks that out-of-set enumeration values
// become "other" and that arrays come back sorted and de-duplicated.
func TestDecodeExtFoldsOutOfSetValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ext  string
		got  func(*Extended) any
		want any
	}{
		{"known distro kept", mut(`"k3s"`, `"talos"`), func(e *Extended) any { return e.Env.Distro }, []string{"talos"}},
		{"unknown distro", mut(`"k3s"`, `"mystery"`), func(e *Extended) any { return e.Env.Distro }, []string{"other"}},
		{"wrong-case distro", mut(`"k3s"`, `"K3S"`), func(e *Extended) any { return e.Env.Distro }, []string{"other"}},
		{"empty distro", mut(`"k3s"`, `""`), func(e *Extended) any { return e.Env.Distro }, []string{"other"}},
		{"k8s minor kept", mut(`"1.31"`, `"1.123"`), func(e *Extended) any { return e.Env.K8s }, []string{"1.123"}},
		{"k8s with v prefix", mut(`"1.31"`, `"v1.31"`), func(e *Extended) any { return e.Env.K8s }, []string{"other"}},
		{"k8s with patch", mut(`"1.31"`, `"1.31.2"`), func(e *Extended) any { return e.Env.K8s }, []string{"other"}},
		{"k8s major 2", mut(`"1.31"`, `"2.0"`), func(e *Extended) any { return e.Env.K8s }, []string{"other"}},
		{"k8s four digit minor", mut(`"1.31"`, `"1.1234"`), func(e *Extended) any { return e.Env.K8s }, []string{"other"}},
		{"k8s empty", mut(`"1.31"`, `""`), func(e *Extended) any { return e.Env.K8s }, []string{"other"}},
		{"arch sorted and de-duplicated", mut(`["amd64"]`, `["riscv64","arm64","amd64","arm64"]`), func(e *Extended) any { return e.Env.Arch }, []string{"amd64", "arm64", "riscv64"}},
		{"arch unknown folds and collapses", mut(`["amd64"]`, `["x86","amd64","sparc","other"]`), func(e *Extended) any { return e.Env.Arch }, []string{"amd64", "other"}},
		{"arch only unknown", mut(`["amd64"]`, `["x86"]`), func(e *Extended) any { return e.Env.Arch }, []string{"other"}},
		{"nodes band kept", mut(`"nodes":"1"`, `"nodes":"11-50"`), func(e *Extended) any { return e.Env.Nodes }, "11-50"},
		{"nodes unknown", mut(`"nodes":"1"`, `"nodes":"7"`), func(e *Extended) any { return e.Env.Nodes }, "other"},
		{"tunnels sorted", mut(`["playit"]`, `["tailscale","frp","playit"]`), func(e *Extended) any { return e.Features.Tunnels }, []string{"frp", "playit", "tailscale"}},
		{"tunnels unknown folds", mut(`["playit"]`, `["wireguard","frp","frp"]`), func(e *Extended) any { return e.Features.Tunnels }, []string{"frp", "other"}},
		{"tunnels empty is non-nil", mut(`["playit"]`, `[]`), func(e *Extended) any { return e.Features.Tunnels }, []string{}},
		{"clusters band kept", mut(`"clusters":"1"`, `"clusters":"11+"`), func(e *Extended) any { return e.Features.Clusters }, "11+"},
		{"clusters unknown", mut(`"clusters":"1"`, `"clusters":"12"`), func(e *Extended) any { return e.Features.Clusters }, "other"},
		{"db postgres kept", mut(`"sqlite"`, `"postgres"`), func(e *Extended) any { return e.Features.DB }, "postgres"},
		{"db unknown", mut(`"sqlite"`, `"mysql"`), func(e *Extended) any { return e.Features.DB }, "other"},
		{"language unknown", mut(`"language":"en"`, `"language":"fr"`), func(e *Extended) any { return e.Features.Language }, "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, err := Decode(withExt(tc.ext))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got := tc.got(r.Ext); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestDecodeExtFoldsUnknownModulesIntoCustom checks that module names outside
// the catalog add their count to games.custom and never appear in Official.
func TestDecodeExtFoldsUnknownModulesIntoCustom(t *testing.T) {
	t.Parallel()
	ext := mut(`"official":{"minecraft-java":2,"terraria":1},"custom":0`,
		`"official":{"minecraft-java":2,"my-private-game":3,"Terraria":1,"look-alike":4},"custom":5`)
	r, _, err := Decode(withExt(ext))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := Games{Official: map[string]int{"minecraft-java": 2}, Custom: 13}
	if !reflect.DeepEqual(r.Ext.Games, want) {
		t.Fatalf("Games = %+v, want %+v", r.Ext.Games, want)
	}

	// An empty official map is valid and non-nil.
	r, _, err = Decode(withExt(mut(`"official":{"minecraft-java":2,"terraria":1}`, `"official":{}`)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if r.Ext.Games.Official == nil || len(r.Ext.Games.Official) != 0 {
		t.Fatalf("Official = %#v, want empty non-nil map", r.Ext.Games.Official)
	}
}

// TestDecodeExtDropsMalformedInstallID checks that a malformed installId drops
// the extended part, keeps the basic report valid and reports ExtDropped.
func TestDecodeExtDropsMalformedInstallID(t *testing.T) {
	t.Parallel()
	ids := map[string]string{
		"empty":            "",
		"words":            "not-a-uuid",
		"uppercase":        strings.ToUpper(testID),
		"mixed case":       "3F1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10",
		"version 1":        "3f1c2a9e-8b4d-1e57-9a61-0c2d7e5b9f10",
		"version 5":        "3f1c2a9e-8b4d-5e57-9a61-0c2d7e5b9f10",
		"variant c":        "3f1c2a9e-8b4d-4e57-ca61-0c2d7e5b9f10",
		"no hyphens":       strings.ReplaceAll(testID, "-", ""),
		"too short":        testID[:35],
		"too long":         testID + "0",
		"non-hex":          "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9fzz",
		"leading space":    " " + testID,
		"trailing newline": testID + "\n",
		"braces":           "{" + testID + "}",
		"urn prefix":       "urn:uuid:" + testID,
		"nil uuid":         "00000000-0000-0000-0000-000000000000",
	}
	for name, id := range ids {
		t.Run(name, func(t *testing.T) {
			r, info, err := Decode(withExt(setAt(t, id, "installId")))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !info.ExtDropped {
				t.Fatal("ExtDropped = false")
			}
			if r.Ext != nil {
				t.Fatalf("Ext = %+v, want nil", r.Ext)
			}
			if r.Version != "0.3.0" || r.Servers != 3 || r.Templates != 7 {
				t.Fatalf("basic part not preserved: %+v", r)
			}
		})
	}

	// Other lowercase UUIDv4 forms are accepted, including variant 8, 9, a, b.
	for _, id := range []string{
		"3f1c2a9e-8b4d-4e57-8a61-0c2d7e5b9f10",
		"3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10",
		"3f1c2a9e-8b4d-4e57-aa61-0c2d7e5b9f10",
		"3f1c2a9e-8b4d-4e57-ba61-0c2d7e5b9f10",
	} {
		r, info, err := Decode(withExt(setAt(t, id, "installId")))
		if err != nil || info.ExtDropped || r.Ext == nil {
			t.Fatalf("id %q: got %+v, %+v, %v; want a kept ext", id, r, info, err)
		}
	}

	// The extended part is validated before it is dropped: a structural error
	// beside a malformed ID is still an error.
	broken := mut(`"installId":"`+testID+`"`, `"installId":"bad"`)
	broken = strings.Replace(broken, `"sso":false`, `"sso":"no"`, 1)
	if _, _, err := Decode(withExt(broken)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("malformed ID with a structural error: err = %v, want ErrInvalidPayload", err)
	}
}

// TestDecodeExtSchema checks the schema gate: 1 is accepted, anything higher
// is ErrUnsupportedSchema, which is also ErrInvalidPayload, and it wins over
// other problems because a newer schema may carry fields this package does not
// know.
func TestDecodeExtSchema(t *testing.T) {
	t.Parallel()
	for name, ext := range map[string]string{
		"schema 4":              setAt(t, 4, "schema"),
		"schema 3":              setAt(t, 3, "schema"),
		"schema 1000":           setAt(t, 1000, "schema"),
		"schema 3 unknown keys": mut(`"schema":1,`, `"schema":3,"newField":{"x":1},`),
		"schema 3 missing keys": `{"schema":3}`,
		"schema 3 bad fields":   strings.Replace(setAt(t, 5, "env"), `"schema":1`, `"schema":3`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Decode(withExt(ext))
			if !errors.Is(err, ErrUnsupportedSchema) {
				t.Fatalf("err = %v, want ErrUnsupportedSchema", err)
			}
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("ErrUnsupportedSchema must also be ErrInvalidPayload: %v", err)
			}
		})
	}
	if !errors.Is(ErrUnsupportedSchema, ErrInvalidPayload) {
		t.Fatal("ErrUnsupportedSchema does not wrap ErrInvalidPayload")
	}
	if _, _, err := Decode(withExt(validExt)); err != nil {
		t.Fatalf("schema 1: %v", err)
	}
}

// TestDecodeExtSentAt checks that sentAt is parsed as RFC 3339, normalised to
// UTC, and that staleness is not Decode's concern.
func TestDecodeExtSentAt(t *testing.T) {
	t.Parallel()
	cases := map[string]time.Time{
		"2026-10-06T09:12:44Z":      time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC),
		"2026-10-06T11:12:44+02:00": time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC),
		"2026-10-06T09:12:44.5Z":    time.Date(2026, 10, 6, 9, 12, 44, 500_000_000, time.UTC),
		"1999-01-01T00:00:00Z":      time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		"2999-01-01T00:00:00Z":      time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		r, _, err := Decode(withExt(setAt(t, in, "sentAt")))
		if err != nil {
			t.Fatalf("sentAt %q: %v", in, err)
		}
		if !r.Ext.SentAt.Equal(want) || r.Ext.SentAt.Location() != time.UTC {
			t.Fatalf("sentAt %q = %v (%v), want %v UTC", in, r.Ext.SentAt, r.Ext.SentAt.Location(), want)
		}
	}
}

// TestEncodeDeterministicRoundTrip checks that Encode is canonical and that
// Decode(Encode(r)) returns r for a valid report.
func TestEncodeDeterministicRoundTrip(t *testing.T) {
	t.Parallel()
	r := Report{
		Version: "0.3.0", Servers: 3, Templates: 7,
		Ext: &Extended{
			Schema:    1,
			InstallID: testID,
			Env:       Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"arm64", "amd64", "arm64"}, Nodes: "1"},
			Games:     Games{Official: map[string]int{"terraria": 1, "minecraft-java": 2}, Custom: 0},
			Features: Features{
				WakeOnConnect: true, Tunnels: []string{"playit", "frp"}, Backups: true,
				Clusters: "1", DB: "sqlite", Language: "en",
			},
			Key:    testKey,
			SentAt: time.Date(2026, 10, 6, 11, 12, 44, 0, time.FixedZone("CEST", 2*3600)),
		},
	}
	const want = `{"version":"0.3.0","servers":3,"templates":7,"ext":{"schema":1,"installId":"` + testID + `",` +
		`"env":{"k8s":"1.31","distro":"k3s","arch":["amd64","arm64"],"nodes":"1"},` +
		`"games":{"official":{"minecraft-java":2,"terraria":1},"custom":0},` +
		`"features":{"wakeOnConnect":true,"tunnels":["frp","playit"],"capture":false,"backups":true,` +
		`"sso":false,"auditForwarding":false,"clusters":"1","db":"sqlite","language":"en"},` +
		`"key":"` + testKey + `","sentAt":"2026-10-06T09:12:44Z"}}`

	for i := 0; i < 20; i++ {
		got, err := Encode(r)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if string(got) != want {
			t.Fatalf("Encode #%d =\n%s\nwant\n%s", i, got, want)
		}
	}
	// Encode must not reorder the caller's slices.
	if !reflect.DeepEqual(r.Ext.Env.Arch, []string{"arm64", "amd64", "arm64"}) {
		t.Fatalf("Encode mutated its input: %v", r.Ext.Env.Arch)
	}

	back, info, err := Decode([]byte(want))
	if err != nil || info.ExtDropped {
		t.Fatalf("Decode(Encode(r)): %v, %+v", err, info)
	}
	again, err := Encode(back)
	if err != nil {
		t.Fatalf("Encode(Decode(...)): %v", err)
	}
	if string(again) != want {
		t.Fatalf("round trip changed the body:\n%s\nwant\n%s", again, want)
	}
	wantTime := time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC)
	if !back.Ext.SentAt.Equal(wantTime) {
		t.Fatalf("SentAt = %v, want %v", back.Ext.SentAt, wantTime)
	}
	back.Ext.SentAt = time.Time{}
	wantExt := &Extended{
		Schema: 1, InstallID: testID,
		Env:   Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64", "arm64"}, Nodes: "1", Deployment: "other"},
		Games: Games{Official: map[string]int{"minecraft-java": 2, "terraria": 1}},
		Features: Features{
			WakeOnConnect: true, Tunnels: []string{"frp", "playit"}, Backups: true,
			Clusters: "1", DB: "sqlite", Language: "en",
		},
		Key: testKey,
	}
	if !reflect.DeepEqual(back.Ext, wantExt) {
		t.Fatalf("Ext = %+v, want %+v", back.Ext, wantExt)
	}
}

// TestEncodeBasicAndEmpty covers a basic-only report and nil collections.
func TestEncodeBasicAndEmpty(t *testing.T) {
	t.Parallel()
	got, err := Encode(Report{Version: "0.3.0", Servers: 3, Templates: 7})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if want := `{"version":"0.3.0","servers":3,"templates":7}`; string(got) != want {
		t.Fatalf("basic = %s, want %s", got, want)
	}
	if _, _, err := Decode(got); err != nil {
		t.Fatalf("Decode(basic): %v", err)
	}

	// Nil arrays and maps are written as [] and {}, never null.
	got, err = Encode(Report{Ext: &Extended{Schema: 1}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, frag := range []string{`"arch":[]`, `"tunnels":[]`, `"official":{}`} {
		if !strings.Contains(string(got), frag) {
			t.Fatalf("%s missing from %s", frag, got)
		}
	}
	if strings.Contains(string(got), "null") {
		t.Fatalf("Encode wrote null: %s", got)
	}
}

// TestEncodeRejectsNegativeCounts covers the only validation Encode performs.
func TestEncodeRejectsNegativeCounts(t *testing.T) {
	t.Parallel()
	for _, r := range []Report{{Servers: -1}, {Templates: -1}} {
		if _, err := Encode(r); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("Encode(%+v) err = %v, want ErrInvalidPayload", r, err)
		}
	}
}
