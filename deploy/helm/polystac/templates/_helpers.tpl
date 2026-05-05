{{- define "polystac.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "polystac.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "polystac.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "polystac.labels" -}}
app.kubernetes.io/name: {{ include "polystac.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "polystac.selectorLabels" -}}
app.kubernetes.io/name: {{ include "polystac.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "polystac.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
