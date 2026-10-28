//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

//go:embed testdata/recovery/maintenance.py
var recoveryMaintenance string

type recoveryFixture struct {
	t                            *testing.T
	ns, image, node, allowedCIDR string
}

// TestPanel_OfflineRecovery owns its API, database, archive and login budget.
// It never stops the installed API/operator or restores shared cluster state.
func TestPanel_OfflineRecovery(t *testing.T) {
	t.Parallel()
	e := envInstance
	ctx := t.Context()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	f := &recoveryFixture{t: t, ns: "e2e-recovery-" + suffix}
	report := map[string]any{"fixture": "standalone-sqlite-kind", "namespace": f.ns, "started_utc": time.Now().UTC(), "stage": "bootstrap", "passed": false}
	t.Cleanup(func() {
		report["passed"], report["finished_utc"] = !t.Failed(), time.Now().UTC()
		dir := os.Getenv("GAMEPLANE_RECOVERY_REPORT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Errorf("create recovery report directory: %v", err)
			return
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Errorf("encode recovery report: %v", err)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, f.ns+".json"), data, 0o600); err != nil {
			t.Errorf("write recovery report: %v", err)
		}
	})
	if _, err := e.K8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: f.ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.K8s.CoreV1().Namespaces().Delete(context.Background(), f.ns, metav1.DeleteOptions{}) })
	deploy, err := e.K8s.AppsV1().Deployments("gameplane-system").Get(ctx, "gameplane-api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "api" {
			f.image = c.Image
		}
	}
	if f.image == "" {
		t.Fatal("installed API image unavailable")
	}
	kubernetes, err := e.K8s.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.allowedCIDR = kubernetes.Spec.ClusterIP + "/32"
	if strings.Contains(kubernetes.Spec.ClusterIP, ":") {
		f.allowedCIDR = kubernetes.Spec.ClusterIP + "/128"
	}
	for _, name := range []string{"data", "archive"} {
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: f.ns}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}}}}
		if _, err := e.K8s.CoreV1().PersistentVolumeClaims(f.ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	password := recoveryRandom(t)
	for name, data := range map[string]map[string][]byte{
		"original-key": {"panel.key": recoveryKey(t)}, "wrong-key": {"panel.key": recoveryKey(t)}, "bootstrap": {"password": []byte(password)},
	} {
		if _, err := e.K8s.CoreV1().Secrets(f.ns).Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: data}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "panel", Namespace: f.ns}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "recovery-panel"}, PublishNotReadyAddresses: true, Ports: []corev1.ServicePort{{Port: 8000, TargetPort: intstr.FromInt32(8000)}}}}
	if _, err := e.K8s.CoreV1().Services(f.ns).Create(ctx, service, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.startAPI("initial", "original-key", true)
	pod := f.waitPodReady("initial")
	f.node = pod.Spec.NodeName
	f.startMaintenance(false)
	probeURL := "http://panel." + f.ns + ".svc:8000/healthz"
	// Positive control: a blocked or misrouted Service must not make the later
	// no-HTTP negative case appear to pass.
	e.Eventually(t, 30*time.Second, func() (bool, string) {
		result := f.maintenance("http-check", probeURL)
		return result["status"] == float64(http.StatusOK), "waiting for recovery HTTP monitor positive control"
	})
	report["http_monitor_positive_control"] = true
	bootstrapAdmin := f.login("initial", "admin", password)
	defer bootstrapAdmin.Close()
	adminID := extractIntField(string(f.request(bootstrapAdmin, http.MethodGet, "/users/me", nil, http.StatusOK)), "id")
	adminUserID, err := strconv.ParseInt(adminID, 10, 64)
	if err != nil || adminUserID <= 0 {
		t.Fatal("bootstrap admin has no valid ID")
	}

	// Register the existing kind cluster using stored, encrypted credentials.
	// The panel has no service-account token, so successful dispatch depends on
	// decrypting this registration after recovery.
	clusterID := "fixture-cluster"
	f.request(bootstrapAdmin, http.MethodPost, "/clusters", map[string]any{"name": clusterID, "displayName": "Recovery fixture", "kubeconfig": recoveryKubeconfig(t)}, http.StatusCreated)
	// Bootstrap grants control-plane administration through the local cluster,
	// but remote server access requires an explicit target namespace binding.
	// Discovery is safe before that grant; Healthy also proves the asynchronous
	// registry has loaded the client needed to accept the binding.
	f.waitClusterHealthy(bootstrapAdmin, clusterID)
	f.request(bootstrapAdmin, http.MethodPost, "/users/"+adminID+"/bindings", map[string]any{"roleName": "admin", "cluster": clusterID, "namespace": "gameplane-games"}, http.StatusCreated)
	// A binding change revokes the affected user's sessions. The new cookie,
	// acquired after the grant, must remain live until the backup is taken.
	f.request(bootstrapAdmin, http.MethodGet, "/users/me", nil, http.StatusUnauthorized)
	bootstrapAdmin.Close()
	admin := f.login("initial", "admin", password)
	defer admin.Close()
	roleName := "recovery-reader"
	f.request(admin, http.MethodPost, "/roles", map[string]any{"name": roleName, "description": "Recovery fixture read only", "permissions": []string{"servers:read"}}, http.StatusCreated)
	userPassword := recoveryRandom(t)
	created := f.request(admin, http.MethodPost, "/users", map[string]any{"username": "reader", "password": userPassword, "role": roleName}, http.StatusOK)
	userID := extractIntField(string(created), "id")
	if userID == "" {
		t.Fatal("created reader has no ID")
	}
	// Prove credential-backed dispatch with the scoped administrator, then
	// repeat it using only the reader's explicit target namespace grant.
	gameName, gameUID := recoveryGame(t, suffix)
	serversPath := "/servers?cluster=" + clusterID + "&namespace=gameplane-games"
	f.waitServer(admin, serversPath, gameUID)
	f.request(admin, http.MethodPost, "/users/"+userID+"/bindings", map[string]any{"roleName": roleName, "cluster": clusterID, "namespace": "gameplane-games"}, http.StatusCreated)
	reader := f.login("initial", "reader", userPassword)
	defer reader.Close()
	f.waitServer(reader, serversPath, gameUID)
	sharePath := "/servers/" + gameName + ":shares?cluster=" + clusterID + "&namespace=gameplane-games"
	shareRequest := map[string]any{"canStart": false, "neverExpires": true}
	// The game was created directly in Kubernetes, without a panel owner.
	// Shares remain owner-only even for scoped administrators. Claim this
	// fixture through the authorized transfer endpoint before minting a share.
	f.request(admin, http.MethodPost, sharePath, shareRequest, http.StatusForbidden)
	f.request(admin, http.MethodPost, "/servers/"+gameName+":transfer?cluster="+clusterID+"&namespace=gameplane-games", map[string]any{"userId": adminUserID}, http.StatusNoContent)
	shareBody := f.request(admin, http.MethodPost, sharePath, shareRequest, http.StatusOK)
	var share struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(shareBody, &share); err != nil || share.Token == "" {
		t.Fatal("share token missing")
	}
	public := &APIClient{BaseURL: admin.BaseURL, HTTP: &http.Client{Timeout: 10 * time.Second}}
	f.request(public, http.MethodGet, "/shares/"+share.Token, nil, http.StatusOK)
	for _, original := range []*APIClient{admin, reader} {
		f.request(original, http.MethodGet, "/users/me", nil, http.StatusOK)
	}
	beforeGame := recoveryGameIdentity(t, gameName)
	beforeCounter := recoveryCounter(t, gameName)
	report["game_before"] = beforeGame
	report["counter_before"] = beforeCounter

	// Stop the sole API before SQLite's online-backup API takes the archive.
	admin.Close()
	reader.Close()
	f.stopPod("initial")
	report["stage"] = "backup"
	// API-token issuance and Bearer authentication are not implemented yet.
	// Seed the reserved table offline so removing its restore-time DELETE
	// cannot pass merely because the real session/share checks still reject.
	if f.maintenance("seed-api-token")["api_tokens"] != float64(1) {
		t.Fatal("API-token cleanup fixture was not seeded")
	}
	backup := f.maintenance("backup")
	report["backup"] = backup
	backupTime, err := time.Parse(time.RFC3339Nano, backup["backup_utc"].(string))
	if err != nil {
		t.Fatal("invalid archive recovery-point timestamp")
	}
	if backup["sessions"].(float64) < 2 || backup["api_tokens"] != float64(1) || backup["active_shares"].(float64) < 1 || backup["audit_rows"].(float64) < 1 {
		t.Fatal("backup lacks session/API-token/share/audit fixture evidence")
	}
	// From here on the archive is mounted read-only even in the maintenance Pod.
	f.stopPod("maintenance")
	f.startMaintenance(true)
	f.startAPI("after-backup", "original-key", false)
	f.waitPodReady("after-backup")
	afterBackup := f.login("after-backup", "admin", password)
	f.request(afterBackup, http.MethodPost, "/users", map[string]any{"username": "after-backup", "password": recoveryRandom(t), "role": "viewer"}, http.StatusOK)
	afterBackup.Close()
	f.stopPod("after-backup")
	lossTime := time.Now()
	report["stage"] = "loss-and-restore"
	report["loss_utc"] = lossTime.UTC()
	report["recovery_point_age_at_loss_seconds"] = lossTime.Sub(backupTime).Seconds()
	loss := f.maintenance("loss")
	if loss["working_database_absent"] != true || loss["archive_digest"] != backup["archive_digest"] {
		t.Fatal("loss simulation did not preserve separate archive")
	}
	restored := f.maintenance("restore")
	report["restored_copy"] = restored
	if restored["archive_digest"] != backup["archive_digest"] || restored["audit_digest"] != backup["audit_digest"] || restored["audit_rows"] != backup["audit_rows"] {
		t.Fatal("restore changed original archive or audit evidence")
	}
	if recoveryCounter(t, gameName) <= beforeCounter {
		t.Fatal("game did not continue across panel downtime")
	}

	// Start an HTTP monitor before the wrong-key process. A startup error alone
	// is insufficient evidence if a listener briefly served partial state first.
	report["stage"] = "wrong-key"
	probeCtx, cancelProbe := context.WithCancel(ctx)
	defer cancelProbe()
	type probeResult struct {
		output string
		err    error
	}
	probeDone := make(chan probeResult, 1)
	go func() {
		out, err := f.execMaintenance(probeCtx, "probe", probeURL)
		probeDone <- probeResult{out, err}
	}()
	e.Eventually(t, 30*time.Second, func() (bool, string) {
		result := f.maintenance("probe-ready")
		return result["ready"] == true, "waiting for wrong-key HTTP monitor"
	})
	f.startAPI("wrong-key", "wrong-key", false)
	e.Eventually(t, 90*time.Second, func() (bool, string) {
		p, err := e.K8s.CoreV1().Pods(f.ns).Get(ctx, "wrong-key", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, status := range p.Status.ContainerStatuses {
			if status.Name == "api" && status.State.Terminated != nil {
				return status.State.Terminated.ExitCode != 0, "wrong-key process exited successfully"
			}
		}
		return false, "waiting for wrong-key startup rejection"
	})
	logs, err := e.K8s.CoreV1().Pods(f.ns).GetLogs("wrong-key", &corev1.PodLogOptions{Container: "api"}).DoRaw(ctx)
	if err != nil || !strings.Contains(string(logs), "management encryption key is inactive") {
		t.Fatal("wrong-key startup did not report the expected inactive-key error (logs withheld)")
	}
	f.maintenance("probe-stop")
	var probe map[string]any
	select {
	case result := <-probeDone:
		if result.err != nil || json.Unmarshal([]byte(result.output), &probe) != nil {
			t.Fatal("wrong-key HTTP monitor failed")
		}
	case <-ctx.Done():
		t.Fatal("wrong-key HTTP monitor did not stop")
	}
	if probe["responses"] != float64(0) || probe["stopped"] != true || probe["attempts"].(float64) < 1 {
		t.Fatal("wrong-key process served HTTP or monitor did not finish")
	}
	report["wrong_key_probe"] = probe
	f.stopPod("wrong-key")
	report["stage"] = "restored-acceptance"
	f.startAPI("restored", "original-key", false)
	f.waitPodReady("restored")
	recovered := f.login("restored", "admin", password)
	defer recovered.Close()
	// Reuse both pre-backup cookie jars against the recovered endpoint, without
	// login. Each was accepted immediately before the original API stopped.
	authChecks := map[string]bool{"api_token_rows_deleted": restored["api_tokens"] == float64(0)}
	for name, original := range map[string]*APIClient{"admin_session_rejected": admin, "reader_session_rejected": reader} {
		stale := &APIClient{BaseURL: recovered.BaseURL, HTTP: original.HTTP, CSRF: original.CSRF}
		authChecks[name] = f.status(stale, "/users/me") == http.StatusUnauthorized
	}
	public.BaseURL = recovered.BaseURL
	authChecks["share_rejected"] = f.status(public, "/shares/"+share.Token) == http.StatusNotFound
	bearersInvalidated := true
	for _, passed := range authChecks {
		bearersInvalidated = bearersInvalidated && passed
	}
	report["restored_auth_checks"] = authChecks
	report["restored_bearers_invalidated"] = bearersInvalidated
	if !bearersInvalidated {
		t.Fatalf("restored authentication cleanup failed: %v", authChecks)
	}
	recoveredReader := f.login("restored", "reader", userPassword)
	defer recoveredReader.Close()
	f.waitServer(recoveredReader, serversPath, gameUID)
	f.request(recoveredReader, http.MethodPost, "/servers?cluster="+clusterID+"&namespace=gameplane-games", map[string]any{"metadata": map[string]any{"name": "forbidden"}}, http.StatusForbidden)
	users := f.request(recovered, http.MethodGet, "/users", nil, http.StatusOK)
	if strings.Contains(string(users), `"after-backup"`) || !strings.Contains(string(users), `"reader"`) {
		t.Fatal("restored users do not match the recovery point")
	}
	roles := f.request(recovered, http.MethodGet, "/roles", nil, http.StatusOK)
	if !strings.Contains(string(roles), `"`+roleName+`"`) {
		t.Fatal("custom role missing after recovery")
	}
	bindings := f.request(recovered, http.MethodGet, "/users/"+userID+"/bindings", nil, http.StatusOK)
	if !strings.Contains(string(bindings), `"`+clusterID+`"`) {
		t.Fatal("scoped grant missing after recovery")
	}
	f.request(recovered, http.MethodGet, "/admin/audit", nil, http.StatusOK)
	verification := f.request(recovered, http.MethodGet, "/admin/audit/verify", nil, http.StatusOK)
	var integrity struct {
		OK      bool  `json:"ok"`
		Checked int64 `json:"checked"`
	}
	if json.Unmarshal(verification, &integrity) != nil || !integrity.OK || integrity.Checked < 1 {
		t.Fatal("restored audit chain failed verification")
	}
	report["audit_chain_verified_rows"] = integrity.Checked
	if f.maintenance("archive")["archive_digest"] != backup["archive_digest"] {
		t.Fatal("original archive mutated during recovery")
	}
	afterGame := recoveryGameIdentity(t, gameName)
	if afterGame != beforeGame {
		t.Fatalf("game identity changed during panel recovery: before=%+v after=%+v", beforeGame, afterGame)
	}
	e.Eventually(t, 15*time.Second, func() (bool, string) {
		return recoveryCounter(t, gameName) > beforeCounter, "game counter did not advance"
	})
	report["game_after"], report["credential_backed_dispatch_verified"] = afterGame, true
	report["counter_after"] = recoveryCounter(t, gameName)
	report["rto_seconds"] = time.Since(lossTime).Seconds()
	report["rto_scope"] = "loss through restore, auth invalidation, wrong-key trial, and credential-backed acceptance checks"
	report["stage"] = "complete"
}

func recoveryKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func recoveryRandom(t *testing.T) string { return hex.EncodeToString(recoveryKey(t)) }

func (f *recoveryFixture) status(c *APIClient, path string) int {
	f.t.Helper()
	resp, _, err := c.DoContext(f.t.Context(), http.MethodGet, path, nil)
	if err != nil {
		f.t.Fatal("recovery HTTP status request failed (details withheld)")
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func (f *recoveryFixture) request(c *APIClient, method, path string, body any, want int) []byte {
	f.t.Helper()
	resp, data, err := c.DoContext(f.t.Context(), method, path, body)
	if err != nil {
		f.t.Fatalf("recovery HTTP request failed (details withheld): %s", method)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		f.t.Fatalf("recovery HTTP %s: status=%d want=%d (path/body withheld)", method, resp.StatusCode, want)
	}
	return data
}

func (f *recoveryFixture) login(pod, user, password string) *APIClient {
	f.t.Helper()
	port, stop := envInstance.PortForward(f.t, f.ns, "pod/"+pod, 8000)
	c := &APIClient{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 20 * time.Second}, stop: stop}
	f.t.Cleanup(stop)
	data := f.request(c, http.MethodPost, "/auth/login", map[string]string{"username": user, "password": password}, http.StatusOK)
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(data, &login); err != nil || login.CSRF == "" {
		f.t.Fatal("login response lacked CSRF token")
	}
	c.CSRF = login.CSRF
	return c
}

func recoverySecurity() (*corev1.PodSecurityContext, *corev1.SecurityContext) {
	uid, yes, no := int64(65532), true, false
	return &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &yes, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, &corev1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
}

func (f *recoveryFixture) startAPI(name, key string, bootstrap bool) {
	f.t.Helper()
	psc, sc := recoverySecurity()
	no, mode := false, int32(0o440)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: f.ns, Labels: map[string]string{"app": "recovery-panel"}}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &no, SecurityContext: psc, Containers: []corev1.Container{{Name: "api", Image: f.image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/api"}, Args: []string{"--standalone", "--panel-key-file=/keys/panel.key", "--panel-key-provisioned", "--db-driver=sqlite", "--db-dsn=file:/data/gameplane.db?_pragma=journal_mode(WAL)", "--remote-allowed-cidrs=" + f.allowedCIDR}, SecurityContext: sc, VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}, {Name: "key", MountPath: "/keys", ReadOnly: true}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(8000)}}, PeriodSeconds: 1}, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")}, Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")}}}}, Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}, {Name: "key", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: key, DefaultMode: &mode}}}}}}
	if bootstrap {
		pod.Spec.InitContainers = []corev1.Container{{Name: "bootstrap", Image: f.image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/api", "bootstrap-admin", "--username", "admin"}, SecurityContext: sc, Env: []corev1.EnvVar{{Name: "GAMEPLANE_DB_DSN", Value: "file:/data/gameplane.db?_pragma=journal_mode(WAL)"}, {Name: "GAMEPLANE_ADMIN_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "bootstrap"}, Key: "password"}}}}, VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}}}}
	}
	if _, err := envInstance.K8s.CoreV1().Pods(f.ns).Create(f.t.Context(), pod, metav1.CreateOptions{}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) waitPodReady(name string) *corev1.Pod {
	f.t.Helper()
	var pod *corev1.Pod
	envInstance.Eventually(f.t, 3*time.Minute, func() (bool, string) {
		var err error
		pod, err = envInstance.K8s.CoreV1().Pods(f.ns).Get(f.t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return true, ""
			}
		}
		return false, "waiting for isolated fixture Pod " + name + " (phase=" + string(pod.Status.Phase) + ")"
	})
	return pod
}

func (f *recoveryFixture) stopPod(name string) {
	f.t.Helper()
	if err := envInstance.K8s.CoreV1().Pods(f.ns).Delete(f.t.Context(), name, metav1.DeleteOptions{}); err != nil {
		f.t.Fatal(err)
	}
	envInstance.Eventually(f.t, 90*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().Pods(f.ns).Get(f.t.Context(), name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), "waiting for API termination before offline database operation"
	})
}

func (f *recoveryFixture) startMaintenance(archiveReadOnly bool) {
	psc, sc := recoverySecurity()
	no := false
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "maintenance", Namespace: f.ns}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &no, SecurityContext: psc, Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{f.node}}}}}}}}, Containers: []corev1.Container{{Name: "maintenance", Image: "python:3.12-alpine", Command: []string{"python3", "-c", "import time; time.sleep(1800)"}, SecurityContext: sc, VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}, {Name: "archive", MountPath: "/archive"}}}}, Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}, {Name: "archive", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "archive"}}}}}}
	pod.Spec.Containers[0].VolumeMounts[1].ReadOnly = archiveReadOnly
	pod.Spec.Volumes[1].PersistentVolumeClaim.ReadOnly = archiveReadOnly
	if _, err := envInstance.K8s.CoreV1().Pods(f.ns).Create(f.t.Context(), pod, metav1.CreateOptions{}); err != nil {
		f.t.Fatal(err)
	}
	f.waitPodReady("maintenance")
}

func (f *recoveryFixture) execMaintenance(ctx context.Context, args ...string) (string, error) {
	command := []string{"exec", "-n", f.ns, "maintenance", "--", "python3", "-c", recoveryMaintenance}
	return envInstance.Kubectl(ctx, append(command, args...)...)
}

func (f *recoveryFixture) maintenance(op string, args ...string) map[string]any {
	f.t.Helper()
	out, err := f.execMaintenance(f.t.Context(), append([]string{op}, args...)...)
	if err != nil {
		f.t.Fatalf("offline recovery operation %s failed (output withheld)", op)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		f.t.Fatalf("decode offline recovery operation %s", op)
	}
	return result
}

func recoveryKubeconfig(t *testing.T) string {
	t.Helper()
	cfg := rest.CopyConfig(envInstance.Cfg)
	if err := rest.LoadTLSFiles(cfg); err != nil {
		t.Fatal("could not load kind TLS fixture")
	}
	if len(cfg.CAData) == 0 || len(cfg.CertData) == 0 || len(cfg.KeyData) == 0 {
		t.Fatal("recovery drill requires kind client certificate credentials")
	}
	kubeconfig := clientcmdapi.Config{Clusters: map[string]*clientcmdapi.Cluster{"kind": {Server: "https://kubernetes.default.svc", CertificateAuthorityData: cfg.CAData}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"kind": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData}}, Contexts: map[string]*clientcmdapi.Context{"kind": {Cluster: "kind", AuthInfo: "kind"}}, CurrentContext: "kind"}
	data, err := clientcmd.Write(kubeconfig)
	if err != nil {
		t.Fatal("could not encode kind TLS fixture")
	}
	return string(data)
}

func (f *recoveryFixture) waitClusterHealthy(c *APIClient, clusterID string) {
	f.t.Helper()
	envInstance.Eventually(f.t, 90*time.Second, func() (bool, string) {
		resp, data, err := c.DoContext(f.t.Context(), http.MethodGet, "/clusters", nil)
		if err != nil {
			return false, fmt.Sprintf("cluster discovery request failed (error type=%T; details withheld)", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("cluster discovery: HTTP status=%d (body withheld)", resp.StatusCode)
		}
		var discovery struct {
			Items []struct {
				Name  string `json:"name"`
				Phase string `json:"phase"`
			} `json:"items"`
		}
		if json.Unmarshal(data, &discovery) != nil {
			return false, "invalid cluster discovery response (body withheld)"
		}
		for _, item := range discovery.Items {
			if item.Name == clusterID && item.Phase == "Healthy" {
				return true, ""
			}
		}
		return false, "registered cluster connection is not Healthy yet"
	})
}

func (f *recoveryFixture) waitServer(c *APIClient, path, uid string) {
	f.t.Helper()
	envInstance.Eventually(f.t, 90*time.Second, func() (bool, string) {
		resp, data, err := c.DoContext(f.t.Context(), http.MethodGet, path, nil)
		if err != nil {
			return false, fmt.Sprintf("credential-backed cluster dispatch failed (error type=%T; details withheld)", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("credential-backed cluster dispatch: HTTP status=%d (path/body withheld)", resp.StatusCode)
		}
		var list struct {
			Items []unstructured.Unstructured `json:"items"`
		}
		if json.Unmarshal(data, &list) != nil {
			return false, "invalid server inventory"
		}
		for _, item := range list.Items {
			if string(item.GetUID()) == uid {
				return true, ""
			}
		}
		return false, "expected live GameServer UID absent from registered cluster"
	})
}

func recoveryGame(t *testing.T, suffix string) (string, string) {
	t.Helper()
	name := "recovery-game-" + suffix
	applyBusyboxTemplate(t, name)
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"command": []string{"sh", "-c", "echo retained > /data/recovery-marker; n=0; while true; do n=$((n+1)); echo $n > /data/recovery-counter.next; mv /data/recovery-counter.next /data/recovery-counter; sleep 1; done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Patch(t.Context(), name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	applyBusyboxGameServer(t, "gameplane-games", name, name)
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods("gameplane-games").Get(t.Context(), name+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, c := range pod.Status.ContainerStatuses {
			if c.Name == "game" && c.State.Running != nil {
				return true, ""
			}
		}
		return false, "waiting for persistent game fixture"
	})
	gs, err := envInstance.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return name, string(gs.GetUID())
}

type recoveryGameState struct {
	PodUID, ContainerID string
	Restarts            int32
}

func recoveryGameIdentity(t *testing.T, name string) recoveryGameState {
	t.Helper()
	pod, err := envInstance.K8s.CoreV1().Pods("gameplane-games").Get(t.Context(), name+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range pod.Status.ContainerStatuses {
		if c.Name == "game" && c.State.Running != nil && c.ContainerID != "" {
			return recoveryGameState{string(pod.UID), c.ContainerID, c.RestartCount}
		}
	}
	t.Fatal("game container is not running")
	return recoveryGameState{}
}

func recoveryCounter(t *testing.T, name string) int {
	t.Helper()
	out, err := envInstance.Kubectl(t.Context(), "exec", "-n", "gameplane-games", name+"-0", "-c", "game", "--", "sh", "-c", "test \"$(cat /data/recovery-marker)\" = retained && cat /data/recovery-counter")
	if err != nil {
		t.Fatal("read persistent game marker/counter")
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatal("invalid game counter")
	}
	return n
}
