// Package controlplane stores standalone panel credentials and remote cluster
// registrations without creating a local Kubernetes cluster.
package controlplane

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

type backend struct {
	db    *sql.DB
	aead  cipher.AEAD
	keyID string
}

type objects struct {
	*backend
	kind string
	ns   string
}

// New opens management storage after Store.Migrate. The encryption key must
// survive alongside the database; a missing key with existing secrets is fatal.
// Kubernetes workload clients are intentionally absent from the result.
func New(ctx context.Context, store *db.Store, keyFile string) (*kube.Client, error) {
	return NewWithOptions(ctx, store, KeyOptions{File: keyFile})
}

// NewWithOptions opens standalone management storage with explicit key custody.
func NewWithOptions(ctx context.Context, store *db.Store, opts KeyOptions) (*kube.Client, error) {
	var count int
	if err := store.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM management_objects WHERE kind = 'secrets') + (SELECT COUNT(*) FROM management_key_state)`).Scan(&count); err != nil {
		return nil, fmt.Errorf("inspect management credentials: %w", err)
	}
	key, err := loadConfiguredKey(opts, count != 0)
	if err != nil {
		return nil, err
	}
	b, err := backendForKey(store.DB, key)
	if err != nil {
		return nil, err
	}
	// Validate every persisted row before serving requests. Wrong keys and
	// corrupt ciphertext fail startup. Keep validation under the same write
	// lock rotation uses, so an old-key process cannot start during retirement.
	if err := b.validateAndInitialize(ctx); err != nil {
		return nil, err
	}
	return &kube.Client{
		SecretStore:     func(ns string) kube.SecretStore { return &secrets{objects{b, "secrets", ns}} },
		ClusterStore:    &clusters{objects{b, "clusters", ""}},
		RegisterCluster: b.registerCluster,
	}, nil
}

func loadKey(path string, required bool) ([]byte, error) {
	return loadKeyWithSync(path, required, syncKeyDirectory)
}

func loadKeyWithSync(path string, required bool, syncDirectory func(string) error) ([]byte, error) {
	if path == "" {
		return nil, errors.New("management key file path is required")
	}
	key, err := readKey(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("management key file must contain exactly 32 bytes")
		}
		// Also persist a concurrent winner's publication, including a retry
		// after its directory sync failed. Never replace its key.
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return nil, fmt.Errorf("sync existing management key directory: %w", err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read management key: %w", err)
	}
	if required {
		return nil, errors.New("management encryption key is missing; restore the original key alongside the database")
	}
	if err := createKeyDirectory(filepath.Dir(path), syncDirectory); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate management key: %w", err)
	}
	// Link a fully written private temporary file into place atomically. An
	// existing destination is never overwritten, including concurrent startup.
	f, err := os.CreateTemp(filepath.Dir(path), ".management-key-*")
	if err != nil {
		return nil, fmt.Errorf("create management key: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write management key: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sync management key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close management key: %w", err)
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadKey(path, true)
		}
		return nil, fmt.Errorf("publish management key: %w", err)
	}
	// Syncing the inode above does not persist its published directory entry.
	// Do not allow encrypted DB writes until the final name survives a crash.
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("sync management key directory: %w", err)
	}
	// Files can inherit extended ACLs from their parent even when created
	// with mode 0600. Apply the same handle-based access checks before first use.
	published, err := readKey(path)
	if err != nil {
		return nil, fmt.Errorf("validate published management key: %w", err)
	}
	if !bytes.Equal(published, key) {
		return nil, errors.New("management key changed during publication")
	}
	return key, nil
}

// readKey confines key access to its configured directory, including symlink
// resolution. The path is operator configuration, never an HTTP request value.
func readKey(path string) ([]byte, error) {
	if err := validateKeyPlatform(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open management key directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := openKeyHandle(root, filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("open management key: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect management key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != 32 {
		return nil, errors.New("management key must be a regular file containing exactly 32 bytes")
	}
	if err := validateKeyFile(info); err != nil {
		return nil, err
	}
	// Validate and read the same open inode; limit even if it grows after Stat.
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return nil, fmt.Errorf("read management key bytes: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("management key changed size while reading")
	}
	return key, nil
}

func (s *objects) resource() schema.GroupResource {
	if s.kind == "clusters" {
		return kube.GVRCluster.GroupResource()
	}
	return schema.GroupResource{Resource: "secrets"}
}

func (s *objects) aad(name, uid string, version int64) []byte {
	return []byte(strings.Join([]string{s.kind, s.ns, name, uid, strconv.FormatInt(version, 10)}, "\x00"))
}

func (s *objects) encode(obj *unstructured.Unstructured, version int64) (string, error) {
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("encode management object: %w", err)
	}
	if s.kind != "secrets" {
		return string(data), nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, data, s.ciphertextAAD(obj.GetName(), string(obj.GetUID()), version))
	return s.ciphertextHeader() + base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *objects) decode(name, uid string, version int64, payload string) (*unstructured.Unstructured, error) {
	data := []byte(payload)
	if s.kind == "secrets" {
		var err error
		data, err = s.openCiphertext(name, uid, version, payload)
		if err != nil {
			return nil, err
		}
	}
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, &obj.Object); err != nil {
		return nil, errors.New("invalid management object data")
	}
	if obj.GetName() != name || obj.GetNamespace() != s.ns || string(obj.GetUID()) != uid || obj.GetResourceVersion() != strconv.FormatInt(version, 10) {
		return nil, errors.New("management object metadata does not match stored identity")
	}
	return obj, nil
}

func (s *objects) get(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	var uid, payload string
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT uid, version, payload FROM management_objects WHERE kind = ? AND namespace = ? AND name = ?`, s.kind, s.ns, name).Scan(&uid, &version, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierrors.NewNotFound(s.resource(), name)
	}
	if err != nil {
		return nil, fmt.Errorf("read management object: %w", err)
	}
	return s.decode(name, uid, version, payload)
}

func (s *objects) list(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	selector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, apierrors.NewBadRequest("invalid label selector")
	}
	if opts.FieldSelector != "" || opts.Watch || opts.Limit < 0 {
		return nil, apierrors.NewBadRequest("unsupported management list options")
	}
	var after string
	if opts.Continue != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(opts.Continue)
		if err != nil {
			return nil, apierrors.NewBadRequest("invalid continuation token")
		}
		after = string(decoded)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, uid, version, payload FROM management_objects WHERE kind = ? AND namespace = ? AND name > ? ORDER BY name`, s.kind, s.ns, after)
	if err != nil {
		return nil, fmt.Errorf("list management objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{}}
	for rows.Next() {
		var name, uid, payload string
		var version int64
		if err := rows.Scan(&name, &uid, &version, &payload); err != nil {
			return nil, fmt.Errorf("read management object: %w", err)
		}
		obj, err := s.decode(name, uid, version, payload)
		if err != nil {
			return nil, err
		}
		if !selector.Matches(labels.Set(obj.GetLabels())) {
			continue
		}
		if opts.Limit > 0 && int64(len(result.Items)) == opts.Limit {
			result.SetContinue(base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].GetName())))
			break
		}
		result.Items = append(result.Items, *obj)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list management objects: %w", err)
	}
	return result, nil
}

func (s *objects) save(ctx context.Context, obj *unstructured.Unstructured, create bool) (*unstructured.Unstructured, error) {
	obj = obj.DeepCopy()
	name := obj.GetName()
	if len(validation.IsDNS1123Subdomain(name)) != 0 {
		return nil, apierrors.NewBadRequest("a valid management object name is required")
	}
	if obj.GetNamespace() != "" && obj.GetNamespace() != s.ns {
		return nil, apierrors.NewBadRequest("namespace does not match management store")
	}
	obj.SetNamespace(s.ns)
	version := int64(1)
	if create {
		if obj.GetResourceVersion() != "" {
			return nil, apierrors.NewBadRequest("resourceVersion must be empty on create")
		}
		uid := make([]byte, 16)
		if _, err := rand.Read(uid); err != nil {
			return nil, fmt.Errorf("generate management identity: %w", err)
		}
		obj.SetUID(types.UID(hex.EncodeToString(uid)))
		obj.SetCreationTimestamp(metav1.Now())
	} else {
		existing, err := s.get(ctx, name)
		if err != nil {
			return nil, err
		}
		if obj.GetResourceVersion() == "" || obj.GetResourceVersion() != existing.GetResourceVersion() || (obj.GetUID() != "" && obj.GetUID() != existing.GetUID()) {
			return nil, apierrors.NewConflict(s.resource(), name, errors.New("management object changed; reload before updating"))
		}
		previous, err := strconv.ParseInt(existing.GetResourceVersion(), 10, 64)
		if err != nil {
			return nil, errors.New("invalid management object version")
		}
		version = previous + 1
		obj.SetUID(existing.GetUID())
		obj.SetCreationTimestamp(existing.GetCreationTimestamp())
	}
	obj.SetResourceVersion(strconv.FormatInt(version, 10))
	payload, err := s.encode(obj, version)
	if err != nil {
		return nil, err
	}
	var result sql.Result
	if create {
		result, err = s.write(ctx, s.kind == "secrets", `INSERT INTO management_objects (kind, namespace, name, uid, version, payload) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (kind, namespace, name) DO NOTHING`, s.kind, s.ns, name, string(obj.GetUID()), version, payload)
	} else {
		result, err = s.write(ctx, s.kind == "secrets", `UPDATE management_objects SET version = ?, payload = ? WHERE kind = ? AND namespace = ? AND name = ? AND uid = ? AND version = ?`, version, payload, s.kind, s.ns, name, string(obj.GetUID()), version-1)
	}
	if err != nil {
		return nil, fmt.Errorf("persist management object: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect management write: %w", err)
	}
	if n == 0 {
		if create {
			return nil, apierrors.NewAlreadyExists(s.resource(), name)
		}
		return nil, apierrors.NewConflict(s.resource(), name, errors.New("management object changed during update"))
	}
	return obj, nil
}

func (s *objects) delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	obj, err := s.get(ctx, name)
	if err != nil {
		return err
	}
	if p := opts.Preconditions; p != nil {
		if (p.UID != nil && *p.UID != obj.GetUID()) || (p.ResourceVersion != nil && *p.ResourceVersion != obj.GetResourceVersion()) {
			return apierrors.NewConflict(s.resource(), name, errors.New("management object delete precondition failed"))
		}
	}
	result, err := s.write(ctx, s.kind == "secrets", `DELETE FROM management_objects WHERE kind = ? AND namespace = ? AND name = ? AND uid = ? AND version = ?`, s.kind, s.ns, name, string(obj.GetUID()), obj.GetResourceVersion())
	if err != nil {
		return fmt.Errorf("delete management object: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect management deletion: %w", err)
	}
	if n == 0 {
		return apierrors.NewConflict(s.resource(), name, errors.New("management object changed during deletion"))
	}
	return nil
}
