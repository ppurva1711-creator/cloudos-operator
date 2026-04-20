{{/*
Expand the name of the chart.
*/}}
{{- define "module2.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "module2.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "module2.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "module2.labels" -}}
helm.sh/chart: {{ include "module2.chart" . }}
{{ include "module2.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "module2.selectorLabels" -}}
app.kubernetes.io/name: {{ include "module2.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "module2.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "module2.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the appropriate apiVersion for RBAC APIs
*/}}
{{- define "module2.rbac.apiVersion" -}}
{{- if .Capabilities.APIVersions.Has "rbac.authorization.k8s.io/v1" }}
rbac.authorization.k8s.io/v1
{{- else }}
rbac.authorization.k8s.io/v1beta1
{{- end }}
{{- end }}

{{/*
Return the appropriate apiVersion for HPA
*/}}
{{- define "module2.hpa.apiVersion" -}}
{{- if .Capabilities.APIVersions.Has "autoscaling/v2" }}
autoscaling/v2
{{- else }}
autoscaling/v1
{{- end }}
{{- end }}

{{/*
Return operator image repository
*/}}
{{- define "module2.operator.image" -}}
{{- printf "%s:%s" .Values.operator.image.repository .Values.operator.image.tag }}
{{- end }}

{{/*
Return gateway image repository
*/}}
{{- define "module2.gateway.image" -}}
{{- printf "%s:%s" .Values.gateway.image.repository .Values.gateway.image.tag }}
{{- end }}

{{/*
Return namespace
*/}}
{{- define "module2.namespace" -}}
{{- default "orchestrator-system" .Values.global.namespace }}
{{- end }}
