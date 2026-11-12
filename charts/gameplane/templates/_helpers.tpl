{{- /*
gameplane.imageTag resolves the shared image tag: an explicit image.tag wins,
otherwise it falls back to the chart's appVersion so a released chart vX always
pulls images vX with no per-install override. Set image.tag (e.g. "edge") to
track a rolling channel.
*/}}
{{- define "gameplane.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion -}}
{{- end -}}

{{- define "gameplane.operatorImage" -}}
{{- printf "%s/operator:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.apiImage" -}}
{{- printf "%s/api:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.webImage" -}}
{{- printf "%s/web:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.agentImage" -}}
{{- if .Values.operator.agentImage -}}
{{- .Values.operator.agentImage -}}
{{- else -}}
{{- printf "%s/agent:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.auditSyslogBridgeImage" -}}
{{- printf "%s/audit-syslog-bridge:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.telemetryReceiverImage" -}}
{{- printf "%s/telemetry-receiver:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.sentinelImage" -}}
{{- if .Values.operator.sentinelImage -}}
{{- .Values.operator.sentinelImage -}}
{{- else -}}
{{- printf "%s/sentinel:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.tunnelFrpImage" -}}
{{- if .Values.operator.tunnelImages.frp -}}
{{- .Values.operator.tunnelImages.frp -}}
{{- else -}}
{{- printf "%s/tunnel-frp:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.tunnelTailscaleImage" -}}
{{- if .Values.operator.tunnelImages.tailscale -}}
{{- .Values.operator.tunnelImages.tailscale -}}
{{- else -}}
{{- printf "%s/tunnel-tailscale:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.tunnelPlayitImage" -}}
{{- if .Values.operator.tunnelImages.playit -}}
{{- .Values.operator.tunnelImages.playit -}}
{{- else -}}
{{- printf "%s/tunnel-playit:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.mcpServerImage" -}}
{{- printf "%s/mcp-server:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}

{{- define "gameplane.captureImage" -}}
{{- if .Values.capture.image -}}
{{- .Values.capture.image -}}
{{- else -}}
{{- printf "%s/capture-sidecar:%s" .Values.image.registry (include "gameplane.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{- /*
gameplane.labels intentionally omits app.kubernetes.io/name so each
resource can set its own (e.g. "gameplane-api", "gameplane-operator")
without colliding with Deployment selectors.
*/}}
{{- define "gameplane.labels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- /*
gameplane.liveCluster renders "true" when templates are rendered against a live
API server (helm install/upgrade, --dry-run=server) and nothing when rendered
offline (helm template, client dry-run), where lookup always returns empty.
kube-system exists on every cluster, so an empty lookup of it means offline.
*/}}
{{- define "gameplane.liveCluster" -}}
{{- if lookup "v1" "Namespace" "" "kube-system" -}}true{{- end -}}
{{- end -}}

{{- /*
gameplane.mtlsChecksum hashes the rendered mtls.yaml plus the data of any custom
CA or client Secret it references. mtls.yaml does not render those Secrets, so
without them rotating a custom Secret would not roll the API and operator.
*/}}
{{- define "gameplane.mtlsChecksum" -}}
{{- $parts := list (include (print .Template.BasePath "/mtls.yaml") .) -}}
{{- $m := .Values.api.agentMTLS -}}
{{- range $name := list ($m.caSecretRef.name | default "") ($m.clientCertRef.name | default "") -}}
{{- if and $name (ne $name "gameplane-agent-ca") (ne $name "gameplane-agent-client") -}}
{{- $s := lookup "v1" "Secret" $.Release.Namespace $name -}}
{{- $parts = append $parts (toJson (($s).data | default dict)) -}}
{{- end -}}
{{- end -}}
{{- join "\n---\n" $parts | sha256sum -}}
{{- end -}}

{{- /* Absent on releases predating optional gateway installs (--reuse-values). */}}
{{- define "gameplane.apiEnabled" -}}
{{- if hasKey .Values.api "enabled" -}}
{{- .Values.api.enabled -}}
{{- else -}}
true
{{- end -}}
{{- end -}}

{{- /* Preserve the combined default when upgrading with --reuse-values. */}}
{{- define "gameplane.operatorEnabled" -}}
{{- if hasKey .Values.operator "enabled" -}}
{{- .Values.operator.enabled -}}
{{- else -}}
true
{{- end -}}
{{- end -}}

{{- /* Shared PodSpec placement fields; absent keys preserve older reuse-values. */ -}}
{{- define "gameplane.placement" -}}
{{- range $key := list "nodeSelector" "tolerations" "affinity" "topologySpreadConstraints" "priorityClassName" -}}
{{- with index $ $key }}
{{ $key }}: {{ toYaml . | nindent 2 }}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Complete auxiliary overrides; empty objects must not become defaults. */ -}}
{{- define "gameplane.auxiliaryResourceArgs" -}}
{{- range $name := list "agent" "sentinel" "tunnel" "backup" -}}
{{- $key := printf "%sResources" $name -}}
{{- $value := index $ $key -}}
{{- if and (hasKey $ $key) (not (kindIs "invalid" $value)) -}}
{{- if not (kindIs "map" $value) -}}
{{- fail (printf "operator.%s must be a resources object or null" $key) -}}
{{- end }}
- {{ printf "--%s-resources=%s" $name (toJson $value) | quote }}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Resolve new diagnostics defaults for upgrades using older reuse-values.
mergeOverwrite preserves explicit zero/empty values so validation can reject them. */ -}}
{{- define "gameplane.nodeDiagnostics" -}}
{{- $configured := (.Values.capture | default dict).nodeDiagnostics | default dict -}}
{{- mergeOverwrite (dict "enabled" false "namespace" "gameplane-diagnostics" "maxCaptures" 10 "storageQuota" "10Gi" "storageClass" "") $configured | toJson -}}
{{- end -}}

{{- /* Validate every namespace before it can reach RBAC or cache flags. */}}
{{- define "gameplane.namespace" -}}
{{- if or (not (kindIs "string" .)) (gt (len (toString .)) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" (toString .))) -}}
{{- fail "operator Secret scope requires explicit DNS-label namespace names (1-63 lowercase letters, digits or hyphens)" -}}
{{- end -}}
{{- . -}}
{{- end -}}

{{- define "gameplane.operatorGameNamespaces" -}}
{{- $namespaces := list (include "gameplane.namespace" .Values.gamesNamespace) -}}
{{- range (.Values.operator.gameNamespaces | default list) -}}
{{- $namespaces = append $namespaces (include "gameplane.namespace" .) -}}
{{- end -}}
{{- uniq $namespaces | toJson -}}
{{- end -}}

{{- /* Both native components resolve remote-cluster credentials here. Keep
the existing operator override for installations with a dedicated namespace. */ -}}
{{- define "gameplane.credentialsNamespace" -}}
{{- $generated := printf "%s-credentials" .Release.Namespace -}}
{{- if gt (len $generated) 63 -}}
{{- $generated = printf "%s-credentials-%s" (trunc 38 .Release.Namespace | trimSuffix "-") (sha256sum .Release.Namespace | trunc 12) -}}
{{- end -}}
{{- include "gameplane.namespace" (.Values.api.credentialsNamespace | default .Values.operator.controlPlaneNamespace | default $generated) -}}
{{- end -}}

{{- define "gameplane.operatorClusterCredentialsNamespace" -}}
{{- if and (eq (include "gameplane.apiEnabled" .) "true") (not .Values.api.standalone) -}}
{{- include "gameplane.credentialsNamespace" . -}}
{{- else -}}
{{- .Values.operator.controlPlaneNamespace | default .Release.Namespace -}}
{{- end -}}
{{- end -}}

{{- define "gameplane.operatorSecretNamespaces" -}}
{{- $namespaces := include "gameplane.operatorGameNamespaces" . | fromJsonArray -}}
{{- range list (include "gameplane.operatorClusterCredentialsNamespace" .) (.Values.operator.agentCASecretNamespace | default .Release.Namespace) (.Values.operator.moduleNamespace | default .Release.Namespace) -}}
{{- $namespaces = append $namespaces (include "gameplane.namespace" .) -}}
{{- end -}}
{{- uniq $namespaces | toJson -}}
{{- end -}}

{{- /*
gameplane.apiServerEgressPeers renders the NetworkPolicy `to:` peers of the
agent and sentinel apiserver egress rules. An explicit
networkPolicies.apiServerCIDRs list wins. Otherwise it renders the RFC1918 +
link-local fallback plus every address the live cluster reports for the
`kubernetes` Service: its ClusterIPs and its EndpointSlice endpoint addresses.
On k3s and kubeadm single-node installs that endpoint is the node's own IP,
which is often public and outside the fallback; without it heartbeats time
out and every server stays "Starting". Offline renders (helm template, client
dry-run) get empty lookups and keep the plain fallback. Addresses are read at
install/upgrade time, so re-run helm upgrade if the API endpoint moves.
*/}}
{{- define "gameplane.apiServerEgressPeers" -}}
{{- $cidrs := .Values.networkPolicies.apiServerCIDRs | default (list) -}}
{{- if not $cidrs -}}
{{- $cidrs = list "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16" "169.254.0.0/16" -}}
{{- $addrs := list -}}
{{- $svc := lookup "v1" "Service" "default" "kubernetes" -}}
{{- range (dig "spec" "clusterIPs" (list) $svc) -}}
{{- $addrs = append $addrs . -}}
{{- end -}}
{{- $slices := lookup "discovery.k8s.io/v1" "EndpointSlice" "default" "" -}}
{{- range (dig "items" (list) $slices) -}}
{{- if eq (dig "metadata" "labels" "kubernetes.io/service-name" "" .) "kubernetes" -}}
{{- range (dig "endpoints" (list) .) -}}
{{- range (dig "addresses" (list) .) -}}
{{- $addrs = append $addrs . -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range $addrs -}}
{{- if contains ":" . -}}
{{- $cidrs = append $cidrs (printf "%s/128" .) -}}
{{- else -}}
{{- $cidrs = append $cidrs (printf "%s/32" .) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range (uniq $cidrs) }}
- ipBlock: { cidr: {{ . | quote }} }
{{- end -}}
{{- end -}}
