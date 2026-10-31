{{- define "hoofprint.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "hoofprint.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "hoofprint.labels" -}}
app.kubernetes.io/name: {{ include "hoofprint.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "hoofprint.tag" -}}
{{- default .Chart.AppVersion .Values.image.tag -}}
{{- end -}}

{{- define "hoofprint.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (printf "%s-agent" (include "hoofprint.fullname" .)) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "hoofprint.tokenSecretName" -}}
{{- if .Values.agent.existingSecret -}}
{{- .Values.agent.existingSecret -}}
{{- else -}}
{{- printf "%s-agent-token" (include "hoofprint.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "hoofprint.databaseURL" -}}
{{- if .Values.api.databaseURL -}}
{{- .Values.api.databaseURL -}}
{{- else if .Values.postgres.enabled -}}
{{- printf "postgres://%s:%s@%s-postgres:5432/%s?sslmode=disable" .Values.postgres.user .Values.postgres.password (include "hoofprint.fullname" .) .Values.postgres.database -}}
{{- else -}}
memory
{{- end -}}
{{- end -}}

{{- define "hoofprint.controlPlane" -}}
{{- if .Values.agent.controlPlane -}}
{{- .Values.agent.controlPlane -}}
{{- else -}}
{{- printf "%s-api:%d" (include "hoofprint.fullname" .) (int .Values.api.service.grpcPort) -}}
{{- end -}}
{{- end -}}
