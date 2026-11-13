{{/* Release name alone when it already contains the chart name (release "frankie" -> "frankie"). */}}
{{- define "frankie.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "frankie.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* Labels selecting one platform component, e.g. include "frankie.componentLabels" (list . "egress-proxy") */}}
{{- define "frankie.componentLabels" -}}
{{- $root := index . 0 -}}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/component: {{ index . 1 }}
{{- end -}}

{{- define "frankie.image" -}}
{{- $root := index . 0 -}}
{{- $override := index $root.Values.images.overrides (index . 1) -}}
{{- if $override -}}
{{ $override }}
{{- else -}}
{{ $root.Values.images.registry }}/frankie-{{ index . 1 }}:{{ required "images.tag is required, e.g. --set images.tag=sha-<commit>" $root.Values.images.tag }}
{{- end -}}
{{- end -}}

{{/* The control plane's non-secret configuration (internal/controlplane/config.go). */}}
{{- define "frankie.controlplaneConfig" -}}
{{- $full := include "frankie.fullname" . -}}
{{- $a := .Values.controlPlane.agent -}}
{{- $coder := .Values.podResources.coder.limits -}}
{{- $repos := list -}}
{{- range .Values.controlPlane.repos -}}
{{- $repos = append $repos (dict "name" (required "controlPlane.repos[].name is required" .name) "upstream" (required "controlPlane.repos[].upstream is required" .upstream) "keyPrefix" (required "controlPlane.repos[].keyPrefix is required" .keyPrefix) "baseBranch" (default "main" .baseBranch)) -}}
{{- end -}}
{{- $tc := .Values.toolchains -}}
{{- $gate := .Values.controlPlane.gate -}}
{{- $gateLimits := .Values.podResources.commandGate.limits -}}
{{- $cfg := dict
  "repos" $repos
  "intakeLabel" .Values.controlPlane.intake.label
  "pollInterval" .Values.controlPlane.intake.pollInterval
  "temporal" (dict "address" (printf "%s-temporal:7233" $full) "namespace" .Values.temporal.namespace)
  "gatewayAdminURL" (printf "http://%s-llm-gateway:%v" $full .Values.llmGateway.port)
  "agent" (dict
    "namespace" .Values.agents.namespace
    "runnerImage" (include "frankie.image" (list . "runner"))
    "runtimeClass" .Values.agents.runtimeClassName
    "serviceAccount" .Values.agents.serviceAccountName
    "egressProxyURL" (printf "http://%s-egress-proxy.%s.svc:%v" $full .Release.Namespace .Values.egressProxy.port)
    "cpu" (toString $coder.cpu) "memory" (toString $coder.memory)
    "workspaceSize" $a.workspaceSize "podDeadline" $a.podDeadline
    "gitProxyURL" (printf "http://%s-controlplane.%s.svc:%v" $full .Release.Namespace .Values.controlPlane.gitProxyPort)
    "gatewayURL" (printf "http://%s-llm-gateway.%s.svc:%v" $full .Release.Namespace .Values.llmGateway.port)
    "model" $a.model "budgetUSD" $a.budgetUSD
    "sessionTimeout" $a.sessionTimeout "idleTimeout" $a.idleTimeout
    "authorName" $a.authorName "authorEmail" $a.authorEmail)
  "gate" (dict
    "commands" $gate.commands "deadline" $gate.deadline "workspaceSize" $gate.workspaceSize
    "cpu" (toString $gateLimits.cpu) "memory" (toString $gateLimits.memory))
  "toolchains" (dict
    "registryURL" (printf "http://%s-registry.%s.svc:%v" $full .Release.Namespace $tc.registry.port)
    "pushHost" (printf "%s-registry.%s.svc:%v" $full .Release.Namespace $tc.registry.port)
    "pullHost" (printf "localhost:%v" $tc.registry.nodePort)
    "namespace" $tc.build.namespace
    "buildkitImage" $tc.build.buildkitImage
    "runtimeClass" $.Values.agents.runtimeClassName
    "serviceAccount" $tc.build.serviceAccountName
    "egressProxyURL" (printf "http://%s-egress-proxy.%s.svc:%v" $full .Release.Namespace .Values.egressProxy.port)
    "cpu" (toString $tc.build.limits.cpu) "memory" (toString $tc.build.limits.memory)
    "scratchSize" $tc.build.scratchSize "deadline" $tc.build.deadline)
-}}
{{- toPrettyJson $cfg -}}
{{- end -}}

{{/* Prisma settings for LiteLLM: no update check, a writable home for its cache. */}}
{{- define "frankie.llmGatewayPrismaEnv" -}}
- name: CHECKPOINT_DISABLE
  value: "1"
- name: PRISMA_HIDE_UPDATE_MESSAGE
  value: "1"
- name: HOME
  value: /tmp
{{- end -}}
