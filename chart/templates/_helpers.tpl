{{/*
Plugin id, the value of the manifest's `id` at the repo root. It is hardcoded
here because Helm cannot read ainsel-plugin.json; rename it together with the
manifest when copying this template. It is both the Plugin resource's name and
the label AInsel keys a plugin's objects by.
*/}}
{{- define "forgejo.pluginId" -}}
forgejo
{{- end -}}

{{/*
Chart name, overridable for the rare case of two installs of one chart.
*/}}
{{- define "forgejo.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified release-scoped name for the objects that are not the Plugin
resource (which must be named exactly the plugin id).
*/}}
{{- define "forgejo.fullname" -}}
{{- $name := include "forgejo.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Name of the PersistentVolumeClaim holding the plugin's install store. It is the
fullname with a suffix, not the fullname itself: the Deployment, the Service and
the NetworkPolicy already carry that name, and a volume that shares it reads as
a second object for the same thing.
*/}}
{{- define "forgejo.pvcName" -}}
{{ printf "%s-data" (include "forgejo.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
Common labels. `ainsel.dev/plugin` is what identifies the objects as belonging
to this plugin, the rest are the Kubernetes recommended labels.
*/}}
{{- define "forgejo.labels" -}}
app.kubernetes.io/name: {{ include "forgejo.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: plugin
app.kubernetes.io/part-of: ainsel
app.kubernetes.io/managed-by: {{ .Release.Service }}
ainsel.dev/plugin: {{ include "forgejo.pluginId" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/*
Selector labels. Kept free of the chart version so an upgrade never changes
a selector, and used both by the Deployment and by the NetworkPolicy, which has
to keep selecting the pods after a version bump. The label AInsel selects plugin
pods by is deliberately not here -- see forgejo.podLabels.
*/}}
{{- define "forgejo.selectorLabels" -}}
app.kubernetes.io/name: {{ include "forgejo.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
ainsel.dev/plugin: {{ include "forgejo.pluginId" . }}
{{- end -}}

{{/*
Labels for the pods themselves: the common labels plus ainsel.io/plugin, which is
what marks a pod as a plugin for AInsel. AInsel's own NetworkPolicy selects on it
and admits the hub backend and the event-source-gateway operator to the service
port, so a pod without the label gets no traffic at all. It is a pod label only:
a selector cannot change once the Deployment exists.
*/}}
{{- define "forgejo.podLabels" -}}
{{ include "forgejo.labels" . }}
ainsel.io/plugin: "true"
{{- end -}}
