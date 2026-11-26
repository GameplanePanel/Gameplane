package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// newTestCA generates a self-signed CA for signing test server certs.
func newTestCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return cert, key, caPEM
}

func parseTestCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func TestWorkerDNSNames(t *testing.T) {
	got := workerDNSNames("exp-abc", "games")
	want := []string{
		"exp-abc",
		"exp-abc.games",
		"exp-abc.games.svc",
		"exp-abc.games.svc.cluster.local",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workerDNSNames = %v, want %v", got, want)
	}
}

func TestBuildArchiveWorkerTLSSecret(t *testing.T) {
	sec := buildArchiveWorkerTLSSecret("exp-abc", "games", []byte("CERT"), []byte("KEY"), []byte("CA"))
	if sec.Name != "exp-abc" || sec.Namespace != "games" {
		t.Fatalf("name/namespace = %s/%s", sec.Namespace, sec.Name)
	}
	if sec.Type != corev1.SecretTypeTLS {
		t.Fatalf("type = %s, want kubernetes.io/tls", sec.Type)
	}
	if sec.Labels[archiveWorkerLabel] != "tls" {
		t.Fatalf("label %s = %q, want tls", archiveWorkerLabel, sec.Labels[archiveWorkerLabel])
	}
	for k, want := range map[string]string{"tls.crt": "CERT", "tls.key": "KEY", "ca.crt": "CA"} {
		if string(sec.Data[k]) != want {
			t.Fatalf("Data[%s] = %q, want %q", k, sec.Data[k], want)
		}
	}
	if len(sec.Data) != 3 {
		t.Fatalf("Data has %d keys, want 3", len(sec.Data))
	}
}

func basePodParams() archiveWorkerParams {
	return archiveWorkerParams{
		Name:                 "exp-abc",
		Namespace:            "games",
		Mode:                 "export",
		UID:                  "uid-123",
		AgentImage:           "agent:test",
		AgentImagePullPolicy: corev1.PullIfNotPresent,
		Env:                  []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
		VolumeMounts:         []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
		Volumes: []corev1.Volume{{
			Name:         "data",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}},
	}
}

func workerVolumeNames(vols []corev1.Volume) []string {
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		out = append(out, v.Name)
	}
	return out
}

func mountPaths(mounts []corev1.VolumeMount) map[string]corev1.VolumeMount {
	out := make(map[string]corev1.VolumeMount, len(mounts))
	for _, m := range mounts {
		out[m.MountPath] = m
	}
	return out
}

func assertWorkerHardening(t *testing.T, c corev1.Container) {
	t.Helper()
	sc := c.SecurityContext
	if sc == nil {
		t.Fatalf("container %s has no security context", c.Name)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("container %s: RunAsNonRoot not true", c.Name)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 65532 {
		t.Errorf("container %s: RunAsUser = %v, want 65532", c.Name, sc.RunAsUser)
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Errorf("container %s: ReadOnlyRootFilesystem not true", c.Name)
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("container %s: AllowPrivilegeEscalation not false", c.Name)
	}
	if sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		t.Errorf("container %s: capabilities drop = %v, want [ALL]", c.Name, sc.Capabilities)
	}
}

func TestBuildArchiveWorkerPod_Serve(t *testing.T) {
	p := basePodParams()
	p.Serve = true
	p.UID = "uid-123"
	pod := buildArchiveWorkerPod(p)

	if pod.Name != "exp-abc" || pod.Namespace != "games" {
		t.Fatalf("pod name/ns = %s/%s", pod.Namespace, pod.Name)
	}
	if pod.Labels[archiveWorkerLabel] != "export" || pod.Labels[archiveWorkerUIDLabel] != "uid-123" {
		t.Fatalf("labels = %v", pod.Labels)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %s", pod.Spec.RestartPolicy)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("AutomountServiceAccountToken must be false")
	}
	if pod.Spec.EnableServiceLinks == nil || *pod.Spec.EnableServiceLinks {
		t.Errorf("EnableServiceLinks must be false")
	}
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("unexpected init containers: %d", len(pod.Spec.InitContainers))
	}

	wantVols := []string{"tmp", "tls", "data"}
	if got := workerVolumeNames(pod.Spec.Volumes); !reflect.DeepEqual(got, wantVols) {
		t.Fatalf("volumes = %v, want %v", got, wantVols)
	}
	tls := pod.Spec.Volumes[1].VolumeSource.Secret
	if tls == nil || tls.SecretName != "exp-abc" {
		t.Fatalf("tls volume secret = %+v", tls)
	}
	if tls.DefaultMode == nil || *tls.DefaultMode != 0o400 {
		t.Errorf("tls DefaultMode = %v, want 0400", tls.DefaultMode)
	}

	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.Name != "worker" || c.Image != "agent:test" || c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("worker container = %s %s %s", c.Name, c.Image, c.ImagePullPolicy)
	}
	wantArgs := []string{
		"archive-worker", "export", "--uid", "uid-123",
		"--addr", ":9443",
		"--cert", "/tls/tls.crt",
		"--key", "/tls/tls.key",
		"--client-ca", "/tls/ca.crt",
	}
	if !reflect.DeepEqual(c.Args, wantArgs) {
		t.Errorf("args = %v, want %v", c.Args, wantArgs)
	}

	envMap := map[string]string{}
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["FOO"] != "bar" || envMap["XDG_CACHE_HOME"] != "/tmp/restic-cache" {
		t.Errorf("env = %v", envMap)
	}

	mounts := mountPaths(c.VolumeMounts)
	if m, ok := mounts["/tmp"]; !ok || m.Name != "tmp" {
		t.Errorf("missing /tmp mount: %+v", c.VolumeMounts)
	}
	if m, ok := mounts["/tls"]; !ok || m.Name != "tls" || !m.ReadOnly {
		t.Errorf("missing read-only /tls mount: %+v", c.VolumeMounts)
	}
	if m, ok := mounts["/data"]; !ok || m.Name != "data" {
		t.Errorf("missing /data mount: %+v", c.VolumeMounts)
	}
	if _, ok := mounts["/tools"]; ok {
		t.Errorf("unexpected /tools mount without NeedsRestic")
	}

	if len(c.Ports) != 1 || c.Ports[0].Name != "archive" || c.Ports[0].ContainerPort != archiveWorkerPort {
		t.Errorf("ports = %+v", c.Ports)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.TCPSocket == nil {
		t.Fatalf("missing TCP readiness probe")
	}
	if c.ReadinessProbe.TCPSocket.Port != intstr.FromInt(archiveWorkerPort) {
		t.Errorf("readiness port = %v", c.ReadinessProbe.TCPSocket.Port)
	}
	if c.ReadinessProbe.PeriodSeconds != 2 {
		t.Errorf("readiness period = %d, want 2", c.ReadinessProbe.PeriodSeconds)
	}
	if c.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
		t.Errorf("termination message policy = %s", c.TerminationMessagePolicy)
	}
	assertWorkerHardening(t, c)
}

func TestBuildArchiveWorkerPod_CopyWithRestic(t *testing.T) {
	p := basePodParams()
	p.Mode = "copy"
	p.Serve = false
	p.NeedsRestic = true
	p.ResticImage = ""
	pod := buildArchiveWorkerPod(p)

	if pod.Labels[archiveWorkerLabel] != "copy" {
		t.Errorf("mode label = %q", pod.Labels[archiveWorkerLabel])
	}

	wantVols := []string{"tmp", "tools", "data"}
	if got := workerVolumeNames(pod.Spec.Volumes); !reflect.DeepEqual(got, wantVols) {
		t.Fatalf("volumes = %v, want %v", got, wantVols)
	}

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("init containers = %d, want 1", len(pod.Spec.InitContainers))
	}
	ic := pod.Spec.InitContainers[0]
	if ic.Name != "tools" || ic.Image != DefaultResticImage {
		t.Errorf("init container = %s %s, want tools %s", ic.Name, ic.Image, DefaultResticImage)
	}
	if !reflect.DeepEqual(ic.Command, []string{"/bin/cp", "/usr/bin/restic", "/tools/restic"}) {
		t.Errorf("init command = %v", ic.Command)
	}
	if m := mountPaths(ic.VolumeMounts)["/tools"]; m.Name != "tools" {
		t.Errorf("init missing /tools mount: %+v", ic.VolumeMounts)
	}
	assertWorkerHardening(t, ic)

	c := pod.Spec.Containers[0]
	if !reflect.DeepEqual(c.Args, []string{"archive-worker", "copy", "--uid", "uid-123"}) {
		t.Errorf("args = %v", c.Args)
	}
	mounts := mountPaths(c.VolumeMounts)
	if m := mounts["/tools"]; m.Name != "tools" {
		t.Errorf("worker missing /tools mount")
	}
	if _, ok := mounts["/tls"]; ok {
		t.Errorf("unexpected /tls mount when Serve is false")
	}
	if len(c.Ports) != 0 || c.ReadinessProbe != nil {
		t.Errorf("copy worker must not expose ports or readiness probe")
	}
	assertWorkerHardening(t, c)
}

func TestBuildArchiveWorkerPod_CustomResticImage(t *testing.T) {
	p := basePodParams()
	p.NeedsRestic = true
	p.ResticImage = "example.test/restic:9"
	pod := buildArchiveWorkerPod(p)
	if got := pod.Spec.InitContainers[0].Image; got != "example.test/restic:9" {
		t.Errorf("init image = %s", got)
	}
}

func TestBuildArchiveWorkerPod_NoResticNoServe(t *testing.T) {
	pod := buildArchiveWorkerPod(basePodParams())
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("unexpected init containers")
	}
	if got := workerVolumeNames(pod.Spec.Volumes); !reflect.DeepEqual(got, []string{"tmp", "data"}) {
		t.Errorf("volumes = %v", got)
	}
	c := pod.Spec.Containers[0]
	if strings.Contains(strings.Join(c.Args, " "), "--addr") {
		t.Errorf("args should not include --addr when not serving: %v", c.Args)
	}
}

func TestBuildArchiveWorkerService(t *testing.T) {
	svc := buildArchiveWorkerService("exp-abc", "games", "export", "uid-123")
	if svc.Name != "exp-abc" || svc.Namespace != "games" {
		t.Fatalf("name/ns = %s/%s", svc.Namespace, svc.Name)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %s", svc.Spec.Type)
	}
	wantSel := map[string]string{archiveWorkerLabel: "export", archiveWorkerUIDLabel: "uid-123"}
	if !reflect.DeepEqual(svc.Spec.Selector, wantSel) {
		t.Errorf("selector = %v, want %v", svc.Spec.Selector, wantSel)
	}
	if !reflect.DeepEqual(svc.Labels, wantSel) {
		t.Errorf("labels = %v, want %v", svc.Labels, wantSel)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("ports = %d", len(svc.Spec.Ports))
	}
	p := svc.Spec.Ports[0]
	if p.Name != "archive" || p.Port != 9443 || p.TargetPort != intstr.FromInt(9443) {
		t.Errorf("port = %+v", p)
	}
}

func TestSignServerCert(t *testing.T) {
	caCert, caKey, _ := newTestCA(t)
	dns := workerDNSNames("exp-abc", "games")
	validFor := 48 * time.Hour

	certPEM, keyPEM, err := signServerCert(caCert, caKey, "exp-abc-worker", dns, validFor)
	if err != nil {
		t.Fatalf("signServerCert: %v", err)
	}
	if block, _ := pem.Decode(keyPEM); block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Errorf("key PEM not RSA PRIVATE KEY")
	}

	cert := parseTestCert(t, certPEM)
	if cert.Subject.CommonName != "exp-abc-worker" {
		t.Errorf("CN = %q", cert.Subject.CommonName)
	}
	if !reflect.DeepEqual(cert.DNSNames, dns) {
		t.Errorf("DNSNames = %v, want %v", cert.DNSNames, dns)
	}
	if remaining := time.Until(cert.NotAfter); remaining > validFor || remaining < validFor-time.Minute {
		t.Errorf("NotAfter remaining = %v, want about %v", remaining, validFor)
	}
	if err := cert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("cert not signed by CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "exp-abc.games.svc"}); err != nil {
		t.Errorf("verify against CA: %v", err)
	}
}

func TestSignAgentServerCertUnchanged(t *testing.T) {
	caCert, caKey, _ := newTestCA(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "games"},
	}

	certPEM, _, err := signAgentServerCert(caCert, caKey, gs)
	if err != nil {
		t.Fatalf("signAgentServerCert: %v", err)
	}
	cert := parseTestCert(t, certPEM)
	if cert.Subject.CommonName != "mc-agent" {
		t.Errorf("CN = %q, want mc-agent", cert.Subject.CommonName)
	}
	if !reflect.DeepEqual(cert.DNSNames, agentDNSNames(gs)) {
		t.Errorf("DNSNames = %v, want %v", cert.DNSNames, agentDNSNames(gs))
	}
	if remaining := time.Until(cert.NotAfter); remaining < 364*24*time.Hour || remaining > 365*24*time.Hour {
		t.Errorf("NotAfter remaining = %v, want about 365d", remaining)
	}
	if cert.ExtKeyUsage == nil || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want ServerAuth", cert.ExtKeyUsage)
	}
}
