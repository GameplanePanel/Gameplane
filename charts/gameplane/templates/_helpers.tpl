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
