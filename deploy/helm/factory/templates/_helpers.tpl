{{- define "factory.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "factory.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* Labels selecting one platform component, e.g. include "factory.componentLabels" (list . "egress-proxy") */}}
{{- define "factory.componentLabels" -}}
{{- $root := index . 0 -}}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/component: {{ index . 1 }}
{{- end -}}

{{- define "factory.image" -}}
{{- $root := index . 0 -}}
{{ $root.Values.images.registry }}/frankie-{{ index . 1 }}:{{ required "images.tag is required, e.g. --set images.tag=sha-<commit>" $root.Values.images.tag }}
{{- end -}}
