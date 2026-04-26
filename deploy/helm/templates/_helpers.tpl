{{/* Common labels for every resource the chart creates. */}}
{{- define "supportly-agent.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/* Selector labels (subset; never include 'version' here so DaemonSet upgrades work). */}}
{{- define "supportly-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Resolve which Secret name we mount as env. */}}
{{- define "supportly-agent.secretName" -}}
{{- if .Values.existingSecret -}}
{{- .Values.existingSecret -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name -}}
{{- end -}}
{{- end -}}

{{/* Image tag, falling back to the chart's appVersion. */}}
{{- define "supportly-agent.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag -}}
{{- end -}}
