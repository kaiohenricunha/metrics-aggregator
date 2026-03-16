{{/*
Render the metrics-aggregator sidecar container.
Include this in your Deployment template's containers list:

    spec:
      containers:
        - name: {{ .Chart.Name }}
          ...
        {{- include "mychart.metricsAggregatorContainer" . | nindent 8 }}
*/}}
{{- define "mychart.metricsAggregatorContainer" -}}
{{- if .Values.metricsAggregator.enabled }}
- name: metrics-aggregator
  image: "{{ .Values.metricsAggregator.image.repository }}:{{ .Values.metricsAggregator.image.tag }}"
  securityContext:
    runAsNonRoot: true
    runAsUser: 10001
    runAsGroup: 10001
    allowPrivilegeEscalation: false
    readOnlyRootFilesystem: true
    capabilities:
      drop: [ALL]
    seccompProfile:
      type: RuntimeDefault
  ports:
    - containerPort: {{ .Values.metricsAggregator.port }}
      name: metrics
  env:
    - name: METRICS_ENDPOINTS
      value: {{ .Values.metricsAggregator.endpoints | toJson | quote }}
    - name: METRICS_AGGREGATOR_PORT
      value: {{ .Values.metricsAggregator.port | quote }}
    - name: LOG_LEVEL
      value: {{ .Values.metricsAggregator.logLevel | quote }}
  livenessProbe:
    httpGet:
      path: /healthz
      port: {{ .Values.metricsAggregator.port }}
    initialDelaySeconds: 5
    periodSeconds: 10
  readinessProbe:
    httpGet:
      path: /healthz
      port: {{ .Values.metricsAggregator.port }}
    initialDelaySeconds: 3
    periodSeconds: 5
  {{- with .Values.metricsAggregator.resources }}
  resources:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- end }}

{{/*
Render pod annotations for Prometheus discovery.
Include this in your Deployment template's pod metadata:

    template:
      metadata:
        annotations:
          {{- include "mychart.metricsAggregatorAnnotations" . | nindent 10 }}
*/}}
{{- define "mychart.metricsAggregatorAnnotations" -}}
{{- if and .Values.metricsAggregator.enabled (eq .Values.metricsAggregator.discovery "annotations") }}
prometheus.io/scrape: "true"
prometheus.io/port: {{ .Values.metricsAggregator.port | quote }}
prometheus.io/path: "/metrics"
{{- end }}
{{- end }}
