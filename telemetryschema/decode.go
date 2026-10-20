package telemetryschema

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"time"
)

// ErrInvalidPayload is wrapped by every error Decode returns for a body that
// is not a valid report: not exactly one JSON object, an unknown, duplicate or
// missing key, a null or wrongly typed value, a structurally invalid extended
// part, or trailing content.
var ErrInvalidPayload = errors.New("invalid payload")

// ErrUnsupportedSchema is returned when ext.schema is newer than this package
// understands. It wraps ErrInvalidPayload, so errors.Is(err, ErrInvalidPayload)
// is also true and a caller that maps both to 400 needs one check.
var ErrUnsupportedSchema = fmt.Errorf("%w: unsupported ext schema", ErrInvalidPayload)

// SchemaVersion is the extended-tier schema this package reads and writes.
const SchemaVersion = 2

// keySize is the length in bytes of an Ed25519 public key.
const keySize = 32

// DecodeInfo reports non-fatal outcomes of Decode.
type DecodeInfo struct {
	// ExtDropped is true when the body carried an ext part that was discarded
	// because ext.installId is not a lowercase UUIDv4. The returned Report is
	// then a valid basic report with Ext == nil.
	ExtDropped bool
}

// Decode parses a report body that has already been read in full. It accepts
// exactly one JSON object with version, servers and templates present and an
// optional ext object; keys are matched case-sensitively, and unknown keys,
// duplicate keys, null values, wrongly typed values and trailing content are
// rejected at every nesting level. Every ext field is required.
//
// Out-of-set enumeration values are folded to "other", arrays are returned
// sorted and de-duplicated (never nil), and unknown games.official modules are
// added to games.custom. Structural errors wrap ErrInvalidPayload; an
// ext.schema above SchemaVersion returns ErrUnsupportedSchema. Staleness of
// sentAt is the receiver's check, not Decode's.
func Decode(body []byte) (Report, DecodeInfo, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	top, err := readObject(dec, "report")
	if err != nil {
		return Report{}, DecodeInfo{}, err
	}

	// Check for trailing content.
	switch err := dec.Decode(&struct{}{}); {
	case errors.Is(err, io.EOF):
		// exactly one value
	case err != nil:
		return Report{}, DecodeInfo{}, fmt.Errorf("%w: trailing content after the report: %w", ErrInvalidPayload, err)
	default:
		return Report{}, DecodeInfo{}, fmt.Errorf("%w: trailing content after the report", ErrInvalidPayload)
	}

	if err := top.expect([]string{"version", "servers", "templates"}, []string{"ext"}); err != nil {
		return Report{}, DecodeInfo{}, err
	}
	var r Report
	if r.Version, err = asString(top.m["version"], "version"); err != nil {
		return Report{}, DecodeInfo{}, err
	}
	if r.Servers, err = asCount(top.m["servers"], "servers", 0); err != nil {
		return Report{}, DecodeInfo{}, err
	}
	if r.Templates, err = asCount(top.m["templates"], "templates", 0); err != nil {
		return Report{}, DecodeInfo{}, err
	}
	extRaw, hasExt := top.m["ext"]
	if !hasExt {
		return r, DecodeInfo{}, nil
	}
	ext, err := decodeExt(extRaw)
	if err != nil {
		return Report{}, DecodeInfo{}, err
	}
	if ext == nil {
		return r, DecodeInfo{ExtDropped: true}, nil
	}
	r.Ext = ext
	return r, DecodeInfo{}, nil
}

// object is a strictly read JSON object: its raw member values by key.
type object struct {
	what string
	m    map[string]json.RawMessage
}

// readObject reads one JSON object from dec: an opening brace, string keys
// without duplicates, non-null values, and a closing brace. what names the
// object in error messages.
func readObject(dec *json.Decoder, what string) (object, error) {
	// Expect opening brace
	tok, err := dec.Token()
	if err != nil {
		return object{}, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if tok != json.Delim('{') {
		return object{}, fmt.Errorf("%w: %s: expected JSON object", ErrInvalidPayload, what)
	}

	o := object{what: what, m: make(map[string]json.RawMessage)}
	for dec.More() {
		// Get the key
		tok, err := dec.Token()
		if err != nil {
			return object{}, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
		}
		key, ok := tok.(string)
		if !ok {
			return object{}, fmt.Errorf("%w: %s: expected string key", ErrInvalidPayload, what)
		}
		// Check for duplicate keys
		if _, dup := o.m[key]; dup {
			return object{}, fmt.Errorf("%w: %s: duplicate key %q", ErrInvalidPayload, what, key)
		}
		// Read the value raw so its type is validated carefully later.
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return object{}, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
		}
		// Reject null values
		if bytes.Equal(raw, []byte("null")) {
			return object{}, fmt.Errorf("%w: %s: null value for key %q", ErrInvalidPayload, what, key)
		}
		o.m[key] = raw
	}

	// Expect closing brace
	tok, err = dec.Token()
	if err != nil {
		return object{}, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if tok != json.Delim('}') {
		return object{}, fmt.Errorf("%w: %s: expected closing brace", ErrInvalidPayload, what)
	}
	return o, nil
}

// readNested reads raw, a value taken from a parent object, as an object.
func readNested(raw json.RawMessage, what string) (object, error) {
	return readObject(json.NewDecoder(bytes.NewReader(raw)), what)
}

// expect rejects the object when a required key is missing or when it holds a
// key that is neither required nor optional.
func (o object) expect(required, optional []string) error {
	for _, k := range required {
		if _, ok := o.m[k]; !ok {
			return fmt.Errorf("%w: %s: missing required field %q", ErrInvalidPayload, o.what, k)
		}
	}
	for k := range o.m {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return fmt.Errorf("%w: %s: unknown field %q", ErrInvalidPayload, o.what, k)
		}
	}
	return nil
}

// asString decodes raw as a JSON string.
func asString(raw json.RawMessage, what string) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: %s must be string: %w", ErrInvalidPayload, what, err)
	}
	return s, nil
}

// asCount decodes raw as a JSON integer of at least minimum.
func asCount(raw json.RawMessage, what string, minimum int) (int, error) {
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%w: %s must be integer: %w", ErrInvalidPayload, what, err)
	}
	if n < minimum {
		return 0, fmt.Errorf("%w: %s must be at least %d", ErrInvalidPayload, what, minimum)
	}
	return n, nil
}

// asBool decodes raw as a JSON boolean.
func asBool(raw json.RawMessage, what string) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("%w: %s must be boolean: %w", ErrInvalidPayload, what, err)
	}
	return b, nil
}

// asEnumSet decodes raw as a JSON array of strings, folds every member that is
// not in set to "other", and returns the result sorted and de-duplicated. The
// result is never nil. An element that is null or not a string is invalid.
func asEnumSet(raw json.RawMessage, what string, set []string) ([]string, error) {
	var in []*string
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("%w: %s must be an array of strings: %w", ErrInvalidPayload, what, err)
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == nil {
			return nil, fmt.Errorf("%w: %s must not contain null", ErrInvalidPayload, what)
		}
		out = append(out, SanitizeEnum(set, *p))
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// asEnum decodes raw as a JSON string and folds it into set.
func asEnum(raw json.RawMessage, what string, set []string) (string, error) {
	s, err := asString(raw, what)
	if err != nil {
		return "", err
	}
	return SanitizeEnum(set, s), nil
}

// decodePublicKey decodes an unpadded base64url Ed25519 public key.
func decodePublicKey(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("key is not unpadded base64url: %w", err)
	}
	if len(b) != keySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(b), keySize)
	}
	if base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, errors.New("key is not canonical unpadded base64url")
	}
	return b, nil
}

// decodeExt validates and sanitises the ext object. It returns (nil, nil) when
// the whole part must be dropped because installId is malformed; every other
// structural problem is an error, and a schema above SchemaVersion wins over
// all of them.
func decodeExt(raw json.RawMessage) (*Extended, error) {
	o, err := readNested(raw, "ext")
	if err != nil {
		return nil, err
	}
	// The schema is checked first: a newer schema may legitimately carry
	// fields this package does not know.
	if sr, ok := o.m["schema"]; ok {
		n, err := asCount(sr, "ext.schema", 0)
		if err != nil {
			return nil, err
		}
		if n > SchemaVersion {
			return nil, fmt.Errorf("%w: %d", ErrUnsupportedSchema, n)
		}
	}
	required := []string{"schema", "installId", "env", "games", "features", "key", "sentAt"}
	if err := o.expect(required, nil); err != nil {
		return nil, err
	}
	var e Extended
	if e.Schema, err = asCount(o.m["schema"], "ext.schema", 1); err != nil {
		return nil, err
	}
	if e.InstallID, err = asString(o.m["installId"], "ext.installId"); err != nil {
		return nil, err
	}
	if e.Env, err = decodeEnv(o.m["env"], e.Schema); err != nil {
		return nil, err
	}
	if e.Games, err = decodeGames(o.m["games"]); err != nil {
		return nil, err
	}
	if e.Features, err = decodeFeatures(o.m["features"]); err != nil {
		return nil, err
	}
	if e.Key, err = asString(o.m["key"], "ext.key"); err != nil {
		return nil, err
	}
	if _, err := decodePublicKey(e.Key); err != nil {
		return nil, fmt.Errorf("%w: ext.%w", ErrInvalidPayload, err)
	}
	sentAt, err := asString(o.m["sentAt"], "ext.sentAt")
	if err != nil {
		return nil, err
	}
	if e.SentAt, err = time.Parse(time.RFC3339, sentAt); err != nil {
		return nil, fmt.Errorf("%w: ext.sentAt is not RFC 3339: %w", ErrInvalidPayload, err)
	}
	e.SentAt = e.SentAt.UTC()
	// A malformed ID drops the extended part only after the rest has been
	// validated, so the report still counts as basic.
	if !installIDRE.MatchString(e.InstallID) {
		return nil, nil
	}
	return &e, nil
}

// decodeEnv validates ext.env. Schema 1 carries k8s and distro as single
// strings and no deployment: they decode as one-value sets, and Deployment is
// "other" (OD-14). Schema 2 carries k8s and distro as non-empty sets and a
// required deployment (OD-13, OD-15). nodes is one band in both.
func decodeEnv(raw json.RawMessage, schema int) (Env, error) {
	o, err := readNested(raw, "ext.env")
	if err != nil {
		return Env{}, err
	}
	keys := []string{"k8s", "distro", "arch", "nodes"}
	if schema >= 2 {
		keys = append(keys, "deployment")
	}
	if err := o.expect(keys, nil); err != nil {
		return Env{}, err
	}
	var e Env
	if schema >= 2 {
		if e.K8s, err = asK8sSet(o.m["k8s"]); err != nil {
			return Env{}, err
		}
		if e.Distro, err = asEnumSet(o.m["distro"], "ext.env.distro", Distros); err != nil {
			return Env{}, err
		}
		if e.Deployment, err = asEnum(o.m["deployment"], "ext.env.deployment", Deployments); err != nil {
			return Env{}, err
		}
	} else {
		var k8s, distro string
		if k8s, err = asString(o.m["k8s"], "ext.env.k8s"); err != nil {
			return Env{}, err
		}
		if distro, err = asEnum(o.m["distro"], "ext.env.distro", Distros); err != nil {
			return Env{}, err
		}
		e.K8s, e.Distro, e.Deployment = []string{k8sMinor(k8s)}, []string{distro}, other
	}
	if e.Arch, err = asEnumSet(o.m["arch"], "ext.env.arch", Arches); err != nil {
		return Env{}, err
	}
	for _, f := range []struct {
		name string
		set  []string
	}{{"k8s", e.K8s}, {"distro", e.Distro}, {"arch", e.Arch}} {
		if len(f.set) == 0 {
			return Env{}, fmt.Errorf("%w: ext.env.%s must not be empty", ErrInvalidPayload, f.name)
		}
	}
	if e.Nodes, err = asEnum(o.m["nodes"], "ext.env.nodes", NodeBands); err != nil {
		return Env{}, err
	}
	return e, nil
}

// k8sMinor returns v when it is a 1.<minor> version and "other" otherwise.
func k8sMinor(v string) string {
	if K8sMinorRE.MatchString(v) {
		return v
	}
	return other
}

// asK8sSet decodes schema 2's ext.env.k8s: an array of strings, each folded by
// k8sMinor, returned sorted and de-duplicated and never nil. An element that is
// null or not a string is invalid.
func asK8sSet(raw json.RawMessage) ([]string, error) {
	var in []*string
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("%w: ext.env.k8s must be an array of strings: %w", ErrInvalidPayload, err)
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == nil {
			return nil, fmt.Errorf("%w: ext.env.k8s must not contain null", ErrInvalidPayload)
		}
		out = append(out, k8sMinor(*p))
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// decodeGames validates ext.games. Counts of modules outside the catalog are
// added to Custom.
func decodeGames(raw json.RawMessage) (Games, error) {
	o, err := readNested(raw, "ext.games")
	if err != nil {
		return Games{}, err
	}
	if err := o.expect([]string{"official", "custom"}, nil); err != nil {
		return Games{}, err
	}
	g := Games{Official: map[string]int{}}
	if g.Custom, err = asCount(o.m["custom"], "ext.games.custom", 0); err != nil {
		return Games{}, err
	}
	official, err := readNested(o.m["official"], "ext.games.official")
	if err != nil {
		return Games{}, err
	}
	for name, v := range official.m {
		n, err := asCount(v, "ext.games.official."+name, 1)
		if err != nil {
			return Games{}, err
		}
		if IsOfficial(name) {
			g.Official[name] = n
			continue
		}
		if g.Custom > math.MaxInt-n {
			return Games{}, fmt.Errorf("%w: ext.games.custom overflows", ErrInvalidPayload)
		}
		g.Custom += n
	}
	return g, nil
}

// decodeFeatures validates ext.features.
func decodeFeatures(raw json.RawMessage) (Features, error) {
	o, err := readNested(raw, "ext.features")
	if err != nil {
		return Features{}, err
	}
	required := []string{"wakeOnConnect", "tunnels", "capture", "backups", "sso", "auditForwarding", "clusters", "db", "language"}
	if err := o.expect(required, nil); err != nil {
		return Features{}, err
	}
	var f Features
	bools := []struct {
		key string
		dst *bool
	}{
		{"wakeOnConnect", &f.WakeOnConnect},
		{"capture", &f.Capture},
		{"backups", &f.Backups},
		{"sso", &f.SSO},
		{"auditForwarding", &f.AuditForwarding},
	}
	for _, b := range bools {
		if *b.dst, err = asBool(o.m[b.key], "ext.features."+b.key); err != nil {
			return Features{}, err
		}
	}
	if f.Tunnels, err = asEnumSet(o.m["tunnels"], "ext.features.tunnels", Tunnels); err != nil {
		return Features{}, err
	}
	if f.Clusters, err = asEnum(o.m["clusters"], "ext.features.clusters", ClusterBands); err != nil {
		return Features{}, err
	}
	if f.DB, err = asEnum(o.m["db"], "ext.features.db", DBs); err != nil {
		return Features{}, err
	}
	if f.Language, err = asEnum(o.m["language"], "ext.features.language", Languages); err != nil {
		return Features{}, err
	}
	return f, nil
}
