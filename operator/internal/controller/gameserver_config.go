package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// configHashAnnotation is stamped on the pod template whenever the
// GameServer materializes any config. It covers every resolved value,
// so changes that would otherwise not alter the pod spec (e.g. a
// Secret-backed value) still roll the StatefulSet.
const configHashAnnotation = "gameplane.local/config-hash"

// materializedConfig is the result of resolving GameServer.spec.config
// against the referenced template's configSchema.
type materializedConfig struct {
	// env holds the config-derived env vars for the game container, in
	// schema order. Precedence: template env < env < GameServer.spec.env.
	// Password fields appear as SecretKeyRef entries into secretData's
	// Secret rather than carrying the value inline.
	env []corev1.EnvVar
	// secretData holds password-type values destined for the
	// per-server `<gs>-config` Secret, keyed by field name. Pod specs
	// are readable by anyone with pod read access, so secrets must
	// never appear there as plain env values.
	secretData map[string][]byte
	// files holds the rendered configFiles in declaration order. They
	// live in the per-server `<gs>-files` Secret — always a Secret,
	// never a ConfigMap, because any template may embed password-type
	// values.
	files []renderedConfigFile
	// hash fingerprints all resolved values (including secret ones)
	// for configHashAnnotation, so Secret-only changes still roll the
	// StatefulSet. Empty when no config materialized.
	hash string
}

// renderedConfigFile is one configFiles entry after template execution.
type renderedConfigFile struct {
	// key is the Secret data key ("file-<i>", by declaration order).
	key string
	// path is the destination, relative to the data volume mountPath.
	path    string
	content []byte
}

// configSecretName returns the per-GameServer Secret holding
// password-type config values, referenced from the game container via
// SecretKeyRef. Owned by the GameServer so it's GC'd on delete.
func configSecretName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-config"
}

// filesSecretName returns the per-GameServer Secret holding rendered
// configFiles, mounted into the config-init container. Owned by the
// GameServer so it's GC'd on delete.
func filesSecretName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-files"
}

// materializeConfig validates spec.config against the template's
// configSchema and resolves it into container env vars and rendered
// configFiles (target=file values feed only the latter). Defaults are
// applied for absent keys (keeping `kubectl apply` behavior identical
// to the wizard, which pre-fills them); empty optional values are
// skipped entirely so games treat them as unset. Any violation is an
// error — the caller fails the GameServer rather than silently
// dropping user intent.
func materializeConfig(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) (*materializedConfig, error) {
	schema := tmpl.Spec.ConfigSchema
	known := make(map[string]bool, len(schema))
	names := make([]string, 0, len(schema))
	for _, f := range schema {
		known[f.Name] = true
		names = append(names, f.Name)
	}
	var unknown []string
	for k := range gs.Spec.Config {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown config keys %s: template %q accepts [%s]",
			strings.Join(unknown, ", "), tmpl.Name, strings.Join(names, ", "))
	}

	mc := &materializedConfig{}
	var hashParts []string
	// values feeds configFiles templates: every schema field is
	// present ("" when unset) so `{{ if .Values.X }}` guards work
	// while missingkey=error still catches typos.
	values := make(map[string]string, len(schema))
	fileFieldSet := false
	for _, f := range schema {
		values[f.Name] = ""
		val, set := gs.Spec.Config[f.Name]
		if !set {
			val = f.Default
		}
		if val == "" && f.AutoFromMemoryLimit != nil {
			val = autoMemoryValue(gs, tmpl, f.AutoFromMemoryLimit.Percent)
		}
		if val == "" {
			if f.Required {
				return nil, fmt.Errorf("required config field %q has no value and no default", f.Name)
			}
			// Unset optional field: no env var at all, so the game
			// falls back to its own default instead of seeing "".
			continue
		}
		// Type/Target carry kubebuilder defaults ("string"/"env") that
		// only the API server applies, so treat "" as the default here.
		switch f.Type {
		case "int":
			intVal, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("config field %q: %q is not an integer", f.Name, val)
			}
			// Check numeric bounds on int fields.
			if f.Min != nil && intVal < *f.Min {
				return nil, fmt.Errorf("config field %q must be at least %d", f.Name, *f.Min)
			}
			if f.Max != nil && intVal > *f.Max {
				return nil, fmt.Errorf("config field %q must be at most %d", f.Name, *f.Max)
			}
		case "bool":
			if _, err := strconv.ParseBool(val); err != nil {
				return nil, fmt.Errorf("config field %q: %q is not a boolean", f.Name, val)
			}
		case "enum":
			if !slices.Contains(f.Enum, val) {
				return nil, fmt.Errorf("config field %q: %q is not one of [%s]",
					f.Name, val, strings.Join(f.Enum, ", "))
			}
		}
		// Check string length bounds on string and password fields.
		if f.Type == "string" || f.Type == "password" {
			strlen := len(val)
			if f.MinLength != nil && strlen < int(*f.MinLength) {
				return nil, fmt.Errorf("config field %q must be at least %d characters", f.Name, *f.MinLength)
			}
			if f.MaxLength != nil && strlen > int(*f.MaxLength) {
				return nil, fmt.Errorf("config field %q must be at most %d characters", f.Name, *f.MaxLength)
			}
		}
		values[f.Name] = val
		hashParts = append(hashParts, f.Name+"="+val)
		if f.Target == "file" {
			// File-target values exist only inside rendered
			// configFiles — no env var, and password values reach
			// the pod via the `<gs>-files` Secret instead of
			// `<gs>-config`.
			fileFieldSet = true
			continue
		}
		if f.Type == "password" {
			if mc.secretData == nil {
				mc.secretData = map[string][]byte{}
			}
			mc.secretData[f.Name] = []byte(val)
			mc.env = append(mc.env, corev1.EnvVar{
				Name: f.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: configSecretName(gs)},
						Key:                  f.Name,
					},
				},
			})
		} else {
			mc.env = append(mc.env, corev1.EnvVar{Name: f.Name, Value: val})
		}
	}
	if fileFieldSet && len(tmpl.Spec.ConfigFiles) == 0 {
		return nil, fmt.Errorf("config fields target a file but template %q declares no configFiles", tmpl.Name)
	}
	files, err := renderConfigFiles(gs, tmpl, values)
	if err != nil {
		return nil, err
	}
	mc.files = files
	for _, f := range files {
		hashParts = append(hashParts, "file:"+f.path+"="+string(f.content))
	}
	if len(hashParts) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(hashParts, "\n")))
		mc.hash = hex.EncodeToString(sum[:])
	}
	return mc, nil
}

// autoMemoryValue renders percent% of the game container's effective
// memory limit as whole mebibytes ("6144M"), or "" when the resolved
// resources declare no memory limit — the field then stays unset and the
// game keeps its own default, matching an unset optional field.
func autoMemoryValue(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, percent int32,
) string {
	res := effectiveResources(gs, tmpl)
	mem := res.Limits.Memory()
	if mem == nil || mem.IsZero() {
		return ""
	}
	mib := mem.Value() * int64(percent) / 100 / (1 << 20)
	if mib <= 0 {
		return ""
	}
	return strconv.FormatInt(mib, 10) + "M"
}

// renderConfigFiles executes every spec.configFiles template against
// the resolved config values. All failures are errors — a bad path or
// template fails the GameServer rather than materializing a pod that
// silently misses a config file.
func renderConfigFiles(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, values map[string]string,
) ([]renderedConfigFile, error) {
	data := struct {
		Values map[string]string
		Server struct{ Name, Namespace string }
	}{Values: values}
	data.Server.Name = gs.Name
	data.Server.Namespace = gs.Namespace

	var files []renderedConfigFile
	seen := make(map[string]bool, len(tmpl.Spec.ConfigFiles))
	for i, cf := range tmpl.Spec.ConfigFiles {
		if err := validateConfigFilePath(cf.Path); err != nil {
			return nil, fmt.Errorf("configFiles[%d]: %w", i, err)
		}
		if seen[cf.Path] {
			return nil, fmt.Errorf("configFiles[%d]: duplicate path %q", i, cf.Path)
		}
		seen[cf.Path] = true
		t, err := template.New(cf.Path).Option("missingkey=error").Parse(cf.Template)
		if err != nil {
			return nil, fmt.Errorf("configFiles[%d] %q: parse template: %w", i, cf.Path, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("configFiles[%d] %q: render template: %w", i, cf.Path, err)
		}
		files = append(files, renderedConfigFile{
			key:     fmt.Sprintf("file-%d", i),
			path:    cf.Path,
			content: buf.Bytes(),
		})
	}
	return files, nil
}

// validateConfigFilePath enforces that a configFiles path stays inside
// the data volume: relative, clean, and free of ".." segments. The CRD
// carries a matching CEL rule; this re-check keeps the operator
// authoritative and attributes errors to the GameServer.
func validateConfigFilePath(p string) error {
	switch {
	case p == "" || p == ".":
		return fmt.Errorf("path %q is empty", p)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("path %q is absolute; paths are relative to the data mount", p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("path %q must not contain '..'", p)
	case path.Clean(p) != p:
		return fmt.Errorf("path %q is not clean (use %q)", p, path.Clean(p))
	}
	return nil
}

// reconcileConfigSecret keeps the per-server `<gs>-config` Secret in
// step with the materialized password values, deleting it outright when
// no password fields are in play (same lifecycle as the managed
// BackupSchedule).
func (r *GameServerReconciler) reconcileConfigSecret(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, mc *materializedConfig,
) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: configSecretName(gs), Namespace: gs.Namespace},
	}
	if len(mc.secretData) == 0 {
		return r.deleteIfControlledBy(ctx, gs, sec)
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = mc.secretData
		return controllerutil.SetControllerReference(gs, sec, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconcile config Secret %s/%s: %w", gs.Namespace, sec.Name, err)
	}
	return nil
}

// reconcileFilesSecret keeps the per-server `<gs>-files` Secret in step
// with the rendered configFiles, deleting it outright when the template
// declares none (same lifecycle as the config Secret above).
func (r *GameServerReconciler) reconcileFilesSecret(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, mc *materializedConfig,
) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: filesSecretName(gs), Namespace: gs.Namespace},
	}
	if len(mc.files) == 0 {
		return r.deleteIfControlledBy(ctx, gs, sec)
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = make(map[string][]byte, len(mc.files))
		for _, f := range mc.files {
			sec.Data[f.key] = f.content
		}
		return controllerutil.SetControllerReference(gs, sec, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconcile files Secret %s/%s: %w", gs.Namespace, sec.Name, err)
	}
	return nil
}
