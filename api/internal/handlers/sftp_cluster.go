package handlers

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// SFTPClusterMount wires the per-cluster SFTP endpoint routes (spec 040, US4). A nil Sessions skips them.
type SFTPClusterMount struct {
	Sessions *auth.SessionStore
	// Kube reads the host key Secret and checks that a cluster registration exists. It may be nil.
	Kube *kube.Client
	// PublicAddress is sftp.publicAddress from the chart: the panel's own endpoint address (host or host:port).
	PublicAddress string
	// HostKeySecret names the Secret (key host_key) in Namespace that holds the panel's SFTP host key.
	HostKeySecret string
	Namespace     string
}

// sftpClusterDeps is what the SFTP cluster routes need. Functions keep the handlers testable without a cluster.
type sftpClusterDeps struct {
	sessions       *auth.SessionStore
	defaultCluster string
	localAddress   string
	// exists reports whether cluster is a known cluster (the panel's own or a registered remote).
	exists func(ctx context.Context, cluster string) (bool, error)
	// localFingerprint returns the panel's SFTP host key fingerprint, or "" when no key is stored yet.
	localFingerprint func(ctx context.Context) (string, error)
}

// sftpHostKeyDataKey is the Secret key the chart writes the SFTP host key under.
const sftpHostKeyDataKey = "host_key"

var sftpFingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// sftpHostKeyFingerprint returns the OpenSSH SHA256 fingerprint of the public half of a PEM private host key.
func sftpHostKeyFingerprint(pemData []byte) (string, error) {
	raw, err := ssh.ParseRawPrivateKey(pemData)
	if err != nil {
		return "", fmt.Errorf("parse sftp host key: %w", err)
	}
	signer, ok := raw.(crypto.Signer)
	if !ok {
		return "", errors.New("sftp host key has no public half")
	}
	pub, err := ssh.NewPublicKey(signer.Public())
	if err != nil {
		return "", fmt.Errorf("sftp host key public half: %w", err)
	}
	return ssh.FingerprintSHA256(pub), nil
}

// localSFTPFingerprint reads the panel's host key Secret and fingerprints it. A missing Secret or an
// unreadable key is logged and reported as "" so the cluster view still loads.
func localSFTPFingerprint(k *kube.Client, ns, secretName string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if k == nil {
			return "", nil
		}
		secret, err := k.Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read sftp host key secret: %w", err)
		}
		data := secret.Data[sftpHostKeyDataKey]
		if len(data) == 0 {
			return "", nil
		}
		fp, err := sftpHostKeyFingerprint(data)
		if err != nil {
			slog.Warn("sftp: host key secret unreadable, fingerprint not shown", "secret", secretName, "err", err)
			return "", nil
		}
		return fp, nil
	}
}

// clusterExists reports whether name is the panel's own cluster or a Cluster registration.
func clusterExists(defaultID string, k *kube.Client) func(ctx context.Context, name string) (bool, error) {
	return func(ctx context.Context, name string) (bool, error) {
		if name == defaultID {
			return true, nil
		}
		if k == nil {
			return false, nil
		}
		_, err := k.Clusters().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get cluster %s: %w", name, err)
		}
		return true, nil
	}
}

// validSFTPAddress accepts host, host:port, [v6]:port. The address is shown to users, so it carries no path,
// userinfo or scheme.
func validSFTPAddress(addr string) bool {
	if addr == "" || len(addr) > 255 || strings.ContainsAny(addr, " \t/\\@?#") {
		return false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// A bracketed IPv6 literal without a port; any other colon means a bare IPv6 address, which needs brackets.
		if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
			return net.ParseIP(addr[1:len(addr)-1]) != nil
		}
		if strings.Contains(addr, ":") {
			return false
		}
		host, port = addr, ""
	}
	if host == "" {
		return false
	}
	if port != "" {
		n, convErr := strconv.Atoi(port)
		if convErr != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}

// sftpClusterView is the drawer payload: the endpoint and the active service tokens of one cluster.
type sftpClusterView struct {
	Cluster            string                  `json:"cluster"`
	Local              bool                    `json:"local"`
	Address            *string                 `json:"address"`
	HostKeyFingerprint *string                 `json:"hostKeyFingerprint"`
	Tokens             []auth.SFTPServiceToken `json:"tokens"`
}

// sftpTokenCreated is a new service token. Token is the only time the secret is returned.
type sftpTokenCreated struct {
	auth.SFTPServiceToken
	Token string `json:"token"`
}

type sftpEndpointReq struct {
	Address            string `json:"address"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

type sftpClusterHandler struct {
	d sftpClusterDeps
}

// mountSFTPClusterRoutes registers the SFTP routes under /clusters (called from MountClustersWithSFTP,
// because chi panics when /clusters is routed twice). Every route needs cluster:manage (rbac.go).
func mountSFTPClusterRoutes(r chi.Router, d sftpClusterDeps) {
	h := &sftpClusterHandler{d: d}
	r.Get("/{name}/sftp", h.get)
	r.Put("/{name}/sftp", h.putEndpoint)
	r.Post("/{name}/sftp-token", h.createToken)
	r.Delete("/{name}/sftp-token/{id}", h.revokeToken)
}

// cluster refuses token callers, checks the caller and the cluster name, and writes the error itself.
func (h *sftpClusterHandler) cluster(w http.ResponseWriter, req *http.Request) (string, bool) {
	if refuseTokenCaller(w, req) {
		return "", false
	}
	if _, ok := callerID(w, req); !ok {
		return "", false
	}
	name := chi.URLParam(req, "name")
	known, err := h.d.exists(req.Context(), name)
	if err != nil {
		httperr.Write(w, req, err)
		return "", false
	}
	if !known {
		httperr.WriteMessage(w, req, messages.New(httperr.IDNotFound, nil))
		return "", false
	}
	return name, true
}

// endpoint returns the address and fingerprint of cluster: chart values and the host key Secret for the
// panel's own cluster, the stored registration for a remote one.
func (h *sftpClusterHandler) endpoint(ctx context.Context, cluster string) (string, string, error) {
	if cluster == h.d.defaultCluster {
		fp, err := h.d.localFingerprint(ctx)
		if err != nil {
			return "", "", err
		}
		return h.d.localAddress, fp, nil
	}
	ep, found, err := h.d.sessions.GetSFTPClusterEndpoint(ctx, cluster)
	if err != nil || !found {
		return "", "", err
	}
	return ep.Address, ep.HostKeyFingerprint, nil
}

// get returns the endpoint and the active service tokens of a cluster. Secrets never appear.
func (h *sftpClusterHandler) get(w http.ResponseWriter, req *http.Request) {
	name, ok := h.cluster(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	address, fp, err := h.endpoint(ctx, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	tokens, err := h.d.sessions.ListSFTPServiceTokens(ctx, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	view := sftpClusterView{Cluster: name, Local: name == h.d.defaultCluster, Tokens: []auth.SFTPServiceToken{}}
	if address != "" {
		view.Address = &address
	}
	if fp != "" {
		view.HostKeyFingerprint = &fp
	}
	for _, t := range tokens {
		if t.RevokedAt == nil {
			view.Tokens = append(view.Tokens, t)
		}
	}
	writeJSON(w, view)
}

// putEndpoint stores the address and host key fingerprint of a remote cluster's SFTP endpoint.
func (h *sftpClusterHandler) putEndpoint(w http.ResponseWriter, req *http.Request) {
	name, ok := h.cluster(w, req)
	if !ok {
		return
	}
	if name == h.d.defaultCluster {
		httperr.WriteMessage(w, req, messages.New("server.sftp.endpoint_local", nil))
		return
	}
	var body sftpEndpointReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	address := strings.TrimSpace(body.Address)
	if !validSFTPAddress(address) {
		httperr.WriteMessage(w, req, messages.New("server.sftp.endpoint_invalid", nil))
		return
	}
	fp := strings.TrimSpace(body.HostKeyFingerprint)
	if !sftpFingerprintPattern.MatchString(fp) {
		httperr.WriteMessage(w, req, messages.New("server.sftp.fingerprint_invalid", nil))
		return
	}
	ep, err := h.d.sessions.SetSFTPClusterEndpoint(req.Context(), auth.SFTPClusterEndpoint{
		Cluster: name, Address: address, HostKeyFingerprint: fp,
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, ep)
}

// createToken mints a service token for a cluster. The token is returned once. Two active tokens per cluster
// at most, so rotation is create, roll the endpoint, then revoke the old one.
func (h *sftpClusterHandler) createToken(w http.ResponseWriter, req *http.Request) {
	name, ok := h.cluster(w, req)
	if !ok {
		return
	}
	tok, raw, err := h.d.sessions.CreateSFTPServiceToken(req.Context(), name)
	if errors.Is(err, auth.ErrSFTPServiceTokenLimit) {
		httperr.WriteMessage(w, req, messages.New("server.sftp.token_limit_reached", nil))
		return
	}
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, sftpTokenCreated{SFTPServiceToken: tok, Token: raw})
}

// revokeToken revokes one active service token of a cluster. Tokens of other clusters are not found.
func (h *sftpClusterHandler) revokeToken(w http.ResponseWriter, req *http.Request) {
	name, ok := h.cluster(w, req)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	err := h.d.sessions.RevokeSFTPServiceToken(req.Context(), name, id)
	if errors.Is(err, auth.ErrSFTPNotFound) {
		httperr.WriteMessage(w, req, messages.New("server.sftp.token_not_found", nil))
		return
	}
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
