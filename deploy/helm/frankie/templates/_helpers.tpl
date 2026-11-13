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

{{/* FRANKIE_REPOS: comma-separated name=upstream pairs from .Values.controlPlane.repos */}}
{{- define "frankie.repos" -}}
{{- $pairs := list -}}
{{- range .Values.controlPlane.repos -}}
{{- $pairs = append $pairs (printf "%s=%s" (required "controlPlane.repos[].name is required" .name) (required "controlPlane.repos[].upstream is required" .upstream)) -}}
{{- end -}}
{{- join "," $pairs -}}
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
