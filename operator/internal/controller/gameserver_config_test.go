package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func configFixtures(
	schema []gameplanev1alpha1.ConfigField, config map[string]string,
) (*gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games"},
		Spec:       gameplanev1alpha1.GameServerSpec{Config: config},
	}
	tmpl := &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "minecraft"},
		Spec:       gameplanev1alpha1.GameTemplateSpec{ConfigSchema: schema},
	}
	return gs, tmpl
}

func envMap(env []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}

func intPtr(v int64) *int64 {
	return &v
}

func int32Ptr(v int32) *int32 {
	return &v
}

func TestMaterializeConfig_ResolvesValuesAndDefaults(t *testing.T) {
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "TYPE", Type: "enum", Enum: []string{"VANILLA", "PAPER"}, Default: "VANILLA", Required: true},
		{Name: "VERSION", Type: "string", Default: "LATEST", Required: true},
		{Name: "MAX_PLAYERS", Type: "int", Default: "20"},
		{Name: "HARDCORE", Type: "bool"},
		{Name: "MOTD", Type: "string"},
	}, map[string]string{
		"TYPE":     "PAPER",
		"HARDCORE": "true",
	})

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	got := envMap(mc.env)
	want := map[string]string{
		"TYPE":        "PAPER",  // explicit value wins over default
		"VERSION":     "LATEST", // default applied when key absent
		"MAX_PLAYERS": "20",     // optional default still applied
		"HARDCORE":    "true",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["MOTD"]; ok {
		t.Errorf("empty optional field MOTD should not produce an env var")
	}
	if len(mc.env) != len(want) {
		t.Errorf("got %d env vars, want %d: %v", len(mc.env), len(want), got)
	}
	if mc.hash == "" {
		t.Errorf("hash should be set when config materializes")
	}
}

func TestMaterializeConfig_SchemaOrderIsDeterministic(t *testing.T) {
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "B", Type: "string", Default: "2"},
		{Name: "A", Type: "string", Default: "1"},
	}, nil)

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if len(mc.env) != 2 || mc.env[0].Name != "B" || mc.env[1].Name != "A" {
		t.Fatalf("env should follow schema order, got %v", mc.env)
	}
}

func TestMaterializeConfig_HashTracksValues(t *testing.T) {
	schema := []gameplanev1alpha1.ConfigField{{Name: "DIFFICULTY", Type: "string", Default: "easy"}}

	gs1, tmpl := configFixtures(schema, map[string]string{"DIFFICULTY": "hard"})
	gs2, _ := configFixtures(schema, map[string]string{"DIFFICULTY": "easy"})
	gs3, _ := configFixtures(schema, nil) // default resolves to "easy" too

	mc1, err := materializeConfig(gs1, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	mc2, err := materializeConfig(gs2, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	mc3, err := materializeConfig(gs3, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if mc1.hash == mc2.hash {
		t.Errorf("different values should hash differently")
	}
	if mc2.hash != mc3.hash {
		t.Errorf("identical resolved values should hash identically")
	}
}

func TestMaterializeConfig_NoSchemaNoConfig(t *testing.T) {
	gs, tmpl := configFixtures(nil, nil)
	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if len(mc.env) != 0 || mc.hash != "" {
		t.Fatalf("no schema should materialize nothing, got env=%v hash=%q", mc.env, mc.hash)
	}
}

func TestMaterializeConfig_Errors(t *testing.T) {
	cases := []struct {
		name    string
		schema  []gameplanev1alpha1.ConfigField
		config  map[string]string
		files   []gameplanev1alpha1.ConfigFile
		wantErr string
	}{
		{
			name:    "unknown key",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "TYPE", Type: "string"}},
			config:  map[string]string{"TPYE": "PAPER"},
			wantErr: "unknown config keys TPYE",
		},
		{
			name:    "required missing without default",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "WORLD", Type: "string", Required: true}},
			config:  nil,
			wantErr: `required config field "WORLD"`,
		},
		{
			name:    "required explicitly emptied despite default",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "WORLD", Type: "string", Default: "w", Required: true}},
			config:  map[string]string{"WORLD": ""},
			wantErr: `required config field "WORLD"`,
		},
		{
			name:    "bad int",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "MAX_PLAYERS", Type: "int"}},
			config:  map[string]string{"MAX_PLAYERS": "lots"},
			wantErr: "not an integer",
		},
		{
			name:    "bad bool",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "HARDCORE", Type: "bool"}},
			config:  map[string]string{"HARDCORE": "yep"},
			wantErr: "not a boolean",
		},
		{
			name:    "enum violation",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "MODE", Type: "enum", Enum: []string{"survival", "creative"}}},
			config:  map[string]string{"MODE": "hardcore"},
			wantErr: "not one of",
		},
		{
			name:    "file value without configFiles",
			schema:  []gameplanev1alpha1.ConfigField{{Name: "SERVER_CFG", Type: "string", Target: "file"}},
			config:  map[string]string{"SERVER_CFG": "motd=hi"},
			wantErr: "declares no configFiles",
		},
		{
			name:    "bad template syntax",
			files:   []gameplanev1alpha1.ConfigFile{{Path: "server.cfg", Template: "{{ .Values.X"}},
			wantErr: "parse template",
		},
		{
			name:    "template references unknown key",
			files:   []gameplanev1alpha1.ConfigFile{{Path: "server.cfg", Template: "{{ .Values.NOPE }}"}},
			wantErr: "render template",
		},
		{
			name:    "absolute path",
			files:   []gameplanev1alpha1.ConfigFile{{Path: "/etc/passwd", Template: "x"}},
			wantErr: "is absolute",
		},
		{
			name:    "path escapes the data mount",
			files:   []gameplanev1alpha1.ConfigFile{{Path: "../escape.cfg", Template: "x"}},
			wantErr: "must not contain '..'",
		},
		{
			name:    "unclean path",
			files:   []gameplanev1alpha1.ConfigFile{{Path: "cfg//server.cfg", Template: "x"}},
			wantErr: "is not clean",
		},
		{
			name: "duplicate path",
			files: []gameplanev1alpha1.ConfigFile{
				{Path: "server.cfg", Template: "a"},
				{Path: "server.cfg", Template: "b"},
			},
			wantErr: "duplicate path",
		},
		{
			name: "int field below Min",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Min: intPtr(4)},
			},
			config:  map[string]string{"MAX_PLAYERS": "2"},
			wantErr: `config field "MAX_PLAYERS" must be at least 4`,
		},
		{
			name: "int field above Max",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Max: intPtr(64)},
			},
			config:  map[string]string{"MAX_PLAYERS": "100"},
			wantErr: `config field "MAX_PLAYERS" must be at most 64`,
		},
		{
			name: "int field with both Min and Max bounds",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Min: intPtr(4), Max: intPtr(64)},
			},
			config:  map[string]string{"MAX_PLAYERS": "100"},
			wantErr: `config field "MAX_PLAYERS" must be at most 64`,
		},
		{
			name: "required string field empty despite MinLength",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", Required: true, MinLength: int32Ptr(1)},
			},
			config:  map[string]string{"SERVER_NAME": ""},
			wantErr: `required config field "SERVER_NAME"`,
		},
		{
			name: "string field below MinLength",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MinLength: int32Ptr(3)},
			},
			config:  map[string]string{"SERVER_NAME": "ab"},
			wantErr: `config field "SERVER_NAME" must be at least 3 characters`,
		},
		{
			name: "string field above MaxLength",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MaxLength: int32Ptr(64)},
			},
			config:  map[string]string{"SERVER_NAME": "a very long server name that exceeds the maximum allowed length of 64 characters total"},
			wantErr: `config field "SERVER_NAME" must be at most 64 characters`,
		},
		{
			name: "password field respects MinLength",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "ADMIN_PASSWORD", Type: "password", MinLength: int32Ptr(8)},
			},
			config:  map[string]string{"ADMIN_PASSWORD": "short"},
			wantErr: `config field "ADMIN_PASSWORD" must be at least 8 characters`,
		},
		{
			name: "password field respects MaxLength",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "ADMIN_PASSWORD", Type: "password", MaxLength: int32Ptr(32)},
			},
			config:  map[string]string{"ADMIN_PASSWORD": "this is a very long password that exceeds the limit"},
			wantErr: `config field "ADMIN_PASSWORD" must be at most 32 characters`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, tmpl := configFixtures(tc.schema, tc.config)
			tmpl.Spec.ConfigFiles = tc.files
			_, err := materializeConfig(gs, tmpl)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestMaterializeConfig_Constraints(t *testing.T) {
	cases := []struct {
		name   string
		schema []gameplanev1alpha1.ConfigField
		config map[string]string
	}{
		{
			name: "int field at Min boundary is inclusive",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Min: intPtr(4)},
			},
			config: map[string]string{"MAX_PLAYERS": "4"},
		},
		{
			name: "int field at Max boundary is inclusive",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Max: intPtr(64)},
			},
			config: map[string]string{"MAX_PLAYERS": "64"},
		},
		{
			name: "string field at MinLength boundary is inclusive",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MinLength: int32Ptr(5)},
			},
			config: map[string]string{"SERVER_NAME": "hello"},
		},
		{
			name: "string field at MaxLength boundary is inclusive",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MaxLength: int32Ptr(5)},
			},
			config: map[string]string{"SERVER_NAME": "hello"},
		},
		{
			name: "Min and Max constraints on int field both satisfied",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int", Min: intPtr(4), Max: intPtr(64)},
			},
			config: map[string]string{"MAX_PLAYERS": "32"},
		},
		{
			name: "MinLength and MaxLength constraints on string field both satisfied",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MinLength: int32Ptr(3), MaxLength: int32Ptr(10)},
			},
			config: map[string]string{"SERVER_NAME": "myserver"},
		},
		{
			name: "constraint on wrong type is ignored",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MODE", Type: "enum", Enum: []string{"survival"}, Min: intPtr(4)},
			},
			config: map[string]string{"MODE": "survival"},
		},
		{
			name: "Min constraint on string field is ignored",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", Min: intPtr(100)},
			},
			config: map[string]string{"SERVER_NAME": "x"},
		},
		{
			name: "Max constraint on string field is ignored",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", Max: intPtr(5)},
			},
			config: map[string]string{"SERVER_NAME": "very long string"},
		},
		{
			name: "no constraints specified behaves as before",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "MAX_PLAYERS", Type: "int"},
				{Name: "SERVER_NAME", Type: "string"},
			},
			config: map[string]string{"MAX_PLAYERS": "999999", "SERVER_NAME": "x"},
		},
		{
			name: "password field respects MinLength constraint",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "ADMIN_PASSWORD", Type: "password", MinLength: int32Ptr(8)},
			},
			config: map[string]string{"ADMIN_PASSWORD": "abcd1234"},
		},
		{
			name: "password field respects MaxLength constraint",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "ADMIN_PASSWORD", Type: "password", MaxLength: int32Ptr(32)},
			},
			config: map[string]string{"ADMIN_PASSWORD": "password123"},
		},
		{
			name: "optional string field with MinLength but empty value is allowed",
			schema: []gameplanev1alpha1.ConfigField{
				{Name: "SERVER_NAME", Type: "string", MinLength: int32Ptr(1)},
			},
			config: map[string]string{"SERVER_NAME": ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, tmpl := configFixtures(tc.schema, tc.config)
			mc, err := materializeConfig(gs, tmpl)
			if err != nil {
				t.Fatalf("materializeConfig: %v", err)
			}
			if mc == nil {
				t.Errorf("expected materialized config, got nil")
			}
		})
	}
}

func TestMaterializeConfig_PasswordGoesToSecret(t *testing.T) {
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "SERVER_PASS", Type: "password", Required: true},
		{Name: "WORLD", Type: "string", Default: "Midgard"},
	}, map[string]string{"SERVER_PASS": "hunter22"})

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if string(mc.secretData["SERVER_PASS"]) != "hunter22" {
		t.Errorf("secretData[SERVER_PASS] = %q, want the password", mc.secretData["SERVER_PASS"])
	}
	var pass *corev1.EnvVar
	for i := range mc.env {
		if mc.env[i].Name == "SERVER_PASS" {
			pass = &mc.env[i]
		}
		if mc.env[i].Value == "hunter22" {
			t.Errorf("password leaked as inline env value on %s", mc.env[i].Name)
		}
	}
	if pass == nil {
		t.Fatalf("no SERVER_PASS env var")
	}
	if pass.ValueFrom == nil || pass.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("SERVER_PASS should use a SecretKeyRef, got %+v", pass)
	}
	if got := pass.ValueFrom.SecretKeyRef.Name; got != "smp-config" {
		t.Errorf("SecretKeyRef.Name = %q, want smp-config", got)
	}
	if got := pass.ValueFrom.SecretKeyRef.Key; got != "SERVER_PASS" {
		t.Errorf("SecretKeyRef.Key = %q, want SERVER_PASS", got)
	}
}

func TestMaterializeConfig_PasswordValueChangesHash(t *testing.T) {
	schema := []gameplanev1alpha1.ConfigField{{Name: "SERVER_PASS", Type: "password"}}
	gs1, tmpl := configFixtures(schema, map[string]string{"SERVER_PASS": "one"})
	gs2, _ := configFixtures(schema, map[string]string{"SERVER_PASS": "two"})

	mc1, err := materializeConfig(gs1, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	mc2, err := materializeConfig(gs2, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	// The SecretKeyRef env entry is identical for both, so only the
	// hash can roll the pod when a password rotates.
	if mc1.hash == mc2.hash {
		t.Errorf("rotating a password must change the config hash")
	}
}

func TestMaterializeConfig_FileTargetUnsetIsAllowed(t *testing.T) {
	// A declared-but-unset file field must not block servers that
	// don't use it; only a value we would have to drop is an error.
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "SERVER_CFG", Type: "string", Target: "file"},
	}, nil)
	if _, err := materializeConfig(gs, tmpl); err != nil {
		t.Fatalf("unset optional file field should be fine, got: %v", err)
	}
}

func TestMaterializeConfig_FileTargetRendersToFile(t *testing.T) {
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "MOTD", Type: "string", Target: "file", Default: "hello"},
		{Name: "MAX_PLAYERS", Type: "int", Default: "20"},
		{Name: "SERVER_PASS", Type: "password", Target: "file"},
	}, map[string]string{"SERVER_PASS": "hunter22"})
	tmpl.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{
		Path:     "cfg/server.cfg",
		Template: "motd={{ .Values.MOTD }}\nmax={{ .Values.MAX_PLAYERS }}\npass={{ .Values.SERVER_PASS }}\nname={{ .Server.Name }}\n",
	}}

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if len(mc.files) != 1 {
		t.Fatalf("got %d files, want 1", len(mc.files))
	}
	f := mc.files[0]
	if f.key != "file-0" || f.path != "cfg/server.cfg" {
		t.Errorf("file key/path = %q/%q, want file-0/cfg/server.cfg", f.key, f.path)
	}
	want := "motd=hello\nmax=20\npass=hunter22\nname=smp\n"
	if string(f.content) != want {
		t.Errorf("rendered content = %q, want %q", f.content, want)
	}
	env := envMap(mc.env)
	if _, ok := env["MOTD"]; ok {
		t.Errorf("file-target field MOTD must not become an env var")
	}
	if env["MAX_PLAYERS"] != "20" {
		t.Errorf("env-target field MAX_PLAYERS missing, got %v", env)
	}
	for _, e := range mc.env {
		if e.Name == "SERVER_PASS" {
			t.Errorf("file-target password must not appear in env at all, got %+v", e)
		}
	}
	if len(mc.secretData) != 0 {
		t.Errorf("file-target password belongs in the files Secret, not %v", mc.secretData)
	}
	if mc.hash == "" {
		t.Errorf("hash should cover rendered files")
	}
}

func TestMaterializeConfig_UnsetOptionalRendersEmptyInTemplate(t *testing.T) {
	gs, tmpl := configFixtures([]gameplanev1alpha1.ConfigField{
		{Name: "PASSWORD", Type: "string", Target: "file"},
	}, nil)
	tmpl.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{
		Path:     "server.cfg",
		Template: "{{ if .Values.PASSWORD }}password={{ .Values.PASSWORD }}{{ end }}",
	}}

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := string(mc.files[0].content); got != "" {
		t.Errorf("unset optional field should render empty via the if-guard, got %q", got)
	}
}

func TestMaterializeConfig_StaticFileNeedsNoSchema(t *testing.T) {
	gs, tmpl := configFixtures(nil, nil)
	tmpl.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{
		Path:     "eula.txt",
		Template: "eula=true\n",
	}}

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := string(mc.files[0].content); got != "eula=true\n" {
		t.Errorf("static file content = %q", got)
	}
	if mc.hash == "" {
		t.Errorf("hash should be set so adding/removing static files rolls the pod")
	}
}

func TestMaterializeConfig_FileContentChangesHash(t *testing.T) {
	schema := []gameplanev1alpha1.ConfigField{{Name: "MOTD", Type: "string", Target: "file", Default: "hi"}}
	file := func(text string) []gameplanev1alpha1.ConfigFile {
		return []gameplanev1alpha1.ConfigFile{{Path: "server.cfg", Template: text}}
	}

	gs, tmpl := configFixtures(schema, nil)
	tmpl.Spec.ConfigFiles = file("motd={{ .Values.MOTD }}")
	base, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}

	// Same values, different template text: must roll the pod.
	gsB, tmplB := configFixtures(schema, nil)
	tmplB.Spec.ConfigFiles = file("# banner\nmotd={{ .Values.MOTD }}")
	reworded, err := materializeConfig(gsB, tmplB)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if base.hash == reworded.hash {
		t.Errorf("template-text change must change the config hash")
	}

	// Same template, different value: must roll too.
	gsC, tmplC := configFixtures(schema, map[string]string{"MOTD": "yo"})
	tmplC.Spec.ConfigFiles = file("motd={{ .Values.MOTD }}")
	revalued, err := materializeConfig(gsC, tmplC)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if base.hash == revalued.hash {
		t.Errorf("file-target value change must change the config hash")
	}
}

func autoMemSchema(percent int32, def string) []gameplanev1alpha1.ConfigField {
	return []gameplanev1alpha1.ConfigField{{
		Name: "MAX_MEMORY", Type: "string", Default: def,
		AutoFromMemoryLimit: &gameplanev1alpha1.AutoFromMemoryLimit{Percent: percent},
	}}
}

func limitsMem(q string) *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(q)},
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit(t *testing.T) {
	gs, tmpl := configFixtures(autoMemSchema(75, ""), nil)
	gs.Spec.Resources = limitsMem("8Gi")

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := envMap(mc.env)["MAX_MEMORY"]; got != "6144M" {
		t.Errorf("MAX_MEMORY = %q, want 6144M (75%% of 8Gi)", got)
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit_ExplicitValueWins(t *testing.T) {
	gs, tmpl := configFixtures(autoMemSchema(75, ""), map[string]string{"MAX_MEMORY": "2G"})
	gs.Spec.Resources = limitsMem("8Gi")

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := envMap(mc.env)["MAX_MEMORY"]; got != "2G" {
		t.Errorf("MAX_MEMORY = %q, want the explicit 2G over the computed value", got)
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit_DefaultWins(t *testing.T) {
	gs, tmpl := configFixtures(autoMemSchema(75, "3G"), nil)
	gs.Spec.Resources = limitsMem("8Gi")

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := envMap(mc.env)["MAX_MEMORY"]; got != "3G" {
		t.Errorf("MAX_MEMORY = %q, want the schema default 3G over the computed value", got)
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit_TemplateResourcesFallback(t *testing.T) {
	gs, tmpl := configFixtures(autoMemSchema(50, ""), nil)
	tmpl.Spec.Resources = *limitsMem("1536Mi")

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if got := envMap(mc.env)["MAX_MEMORY"]; got != "768M" {
		t.Errorf("MAX_MEMORY = %q, want 768M (50%% of the template's 1536Mi)", got)
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit_NoLimitStaysUnset(t *testing.T) {
	gs, tmpl := configFixtures(autoMemSchema(75, ""), nil)

	mc, err := materializeConfig(gs, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if v, ok := envMap(mc.env)["MAX_MEMORY"]; ok {
		t.Errorf("MAX_MEMORY = %q, want no env var when no memory limit is set", v)
	}
}

func TestMaterializeConfig_AutoFromMemoryLimit_HashTracksLimit(t *testing.T) {
	gsA, tmpl := configFixtures(autoMemSchema(75, ""), nil)
	gsA.Spec.Resources = limitsMem("4Gi")
	gsB, _ := configFixtures(autoMemSchema(75, ""), nil)
	gsB.Spec.Resources = limitsMem("8Gi")

	mcA, err := materializeConfig(gsA, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	mcB, err := materializeConfig(gsB, tmpl)
	if err != nil {
		t.Fatalf("materializeConfig: %v", err)
	}
	if mcA.hash == mcB.hash {
		t.Errorf("a memory-limit change must change the config hash so the pod rolls")
	}
}
