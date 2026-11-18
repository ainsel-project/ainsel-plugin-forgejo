#!/usr/bin/env bash
# Render tests for the forgejo Helm chart.
#
# The chart has one rule that a plain `helm lint` cannot check: it must refuse
# to render at all without image.digest, because AInsel's Plugin resource pins
# the image by digest and a rendering with an empty digest would install a
# plugin that resolves to no image (or to a mutable tag). These tests pin that,
# the objects the chart is made of, the names that have to agree across them,
# the labels and peers AInsel's own platform NetworkPolicy needs to reach a
# plugin pod, the rule that .Values.service.port is the only place a port comes
# from, and the install store: the claim, the mount it is mounted at and the
# $DATA_DIR that points at it.
#
# Usage: bash chart/tests/render_test.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# A real-length digest: AInsel verifies it against the registry, a short stub
# like sha256:abc would let a truncated digest slip through.
DIGEST="sha256:7f3c1a9e4b2d856f0a1c3e5d7b9f13579ace02468bdf13579ace02468bdf1357"
REPOSITORY="ghcr.io/ainsel-project/ainsel-plugin-forgejo"

if ! command -v helm >/dev/null 2>&1; then
  echo "FAIL helm is required to render the chart" >&2
  exit 1
fi

pass() { printf 'PASS %s\n' "$1"; }
fail() {
  printf 'FAIL %s\n' "$1" >&2
  exit 1
}

# render renders the chart with the digest set; extra arguments (values flags,
# --set/--show-only) are passed through.
render() {
  helm template t "$CHART_DIR" --set "image.digest=${DIGEST}" "$@"
}

# --- (a) no digest: the render must fail loudly ------------------------------

if out="$(helm template t "$CHART_DIR" 2>&1)"; then
  fail "helm template without image.digest succeeded, expected it to fail"
fi
case "$out" in
*"image.digest is required"*) pass "render without image.digest fails with 'image.digest is required'" ;;
*) fail "helm template without image.digest failed for another reason: $out" ;;
esac

# --- (b) with a digest: every object renders as AInsel expects ---------------

out="$(render)"

for want in \
  'kind: Deployment' \
  'kind: Service' \
  'kind: NetworkPolicy' \
  'kind: Plugin' \
  'apiVersion: ainsel.dev/v1alpha1' \
  'name: forgejo' \
  "digest: ${DIGEST}" \
  'ainsel.dev/plugin: forgejo' \
  'path: /healthz' \
  'runAsNonRoot: true' \
  'readOnlyRootFilesystem: true' \
  'allowPrivilegeEscalation: false' \
  'seccompProfile:' \
  'type: RuntimeDefault' \
  '- ALL' \
  'name: http' \
  'containerPort: 8080' \
  'value: "8080"'; do
  grep -qF -- "$want" <<<"$out" || fail "render output is missing '${want}'"
  pass "render output contains '${want}'"
done

# The image is pinned by digest, never by a mutable tag.
grep -Eq -- "^[[:space:]]*image: \"?${REPOSITORY}@${DIGEST}\"?$" <<<"$out" ||
  fail "render output does not pin the image to ${REPOSITORY}@${DIGEST}"
pass "render output pins the image to '${REPOSITORY}@${DIGEST}'"

# --- pod template labels -----------------------------------------------------
#
# AInsel's platform NetworkPolicy selects plugin pods by ainsel.io/plugin: "true"
# and admits the hub and the event-gateway operator to them; a pod without the
# label is selected by nothing and gets no traffic at all. It belongs on the pod
# template only: spec.selector.matchLabels is immutable once set, so a label that
# is not part of the identity of the Deployment must never enter it.

deployment="$(render --show-only templates/deployment.yaml || true)"
pod_labels="$(awk '
  /^  template:/ { tpl = 1 }
  tpl && /^    metadata:/ { meta = 1; next }
  meta && /^    spec:/ { exit }
  meta { print }
' <<<"$deployment")"
grep -qE -- 'ainsel\.io/plugin:[[:space:]]*"true"' <<<"$pod_labels" ||
  fail "pod template is not labelled ainsel.io/plugin: \"true\": $pod_labels"
pass "pod template carries the ainsel.io/plugin \"true\" label"

selector_labels="$(awk '
  /^  selector:/ { sel = 1; next }
  sel && /^  template:/ { exit }
  sel { print }
' <<<"$deployment")"
if grep -qF 'ainsel.io/plugin' <<<"$selector_labels"; then
  fail "deployment selector contains ainsel.io/plugin, selectors must stay immutable: $selector_labels"
fi
pass "deployment selector keeps ainsel.io/plugin out (it is not a selector label)"

# The plugin calls the hub with the credential the hub forwarded; the hub's
# address is a value, never a literal in the binary. An empty hub.url leaves the
# env empty, which the binary reads as "hub calls disabled".
grep -qE -- '^[[:space:]]*- name: HUB_URL$' <<<"$deployment" ||
  fail "container has no HUB_URL env: $deployment"
pass "container gets a HUB_URL env"
grep -qF 'value: "http://hub:8080"' <<<"$deployment" ||
  fail "HUB_URL does not default to the hub Service in AInsel's namespace: $deployment"
pass "HUB_URL defaults to http://hub:8080"
grep -qF 'value: "http://hub.example.test:9091"' <(render --set 'hub.url=http://hub.example.test:9091' --show-only templates/deployment.yaml) ||
  fail "hub.url does not override HUB_URL"
pass "hub.url overrides HUB_URL"

# The plugin's install store lives on a volume, because the container's root
# filesystem is read only. $DATA_DIR and the mountPath are the same directory read
# two ways -- the binary looks where the volume is mounted -- so a render that
# moves one without the other would have the plugin write next to its data, not
# into it.
grep -qE -- '^[[:space:]]*- name: DATA_DIR$' <<<"$deployment" ||
  fail "container has no DATA_DIR env: $deployment"
pass "container gets a DATA_DIR env"
grep -qE -- '^[[:space:]]*value: /data$' <<<"$deployment" ||
  fail "DATA_DIR does not point at /data: $deployment"
pass "DATA_DIR points at /data"
mount_path="$(awk '
  /volumeMounts:/ { vm = 1; next }
  vm && /^ +securityContext:/ { exit }
  vm && /mountPath:/ { print $2; exit }
' <<<"$deployment")"
[ "$mount_path" = "/data" ] ||
  fail "the data volume is mounted at '${mount_path}', where DATA_DIR says /data"
pass "the data volume is mounted where DATA_DIR points"

# The Forgejo address the wizard defaults to is a value, never a literal baked
# into the image: an install pointed at another Forgejo sets it and every link the
# plugin shows follows.
grep -qE -- '^[[:space:]]*- name: FORGEJO_BASE_URL$' <<<"$deployment" ||
  fail "container has no FORGEJO_BASE_URL env: $deployment"
pass "container gets a FORGEJO_BASE_URL env"
grep -qE -- '^[[:space:]]*value: "https://forgejo\.example\.test/"$' <(render --set 'forgejo.baseURL=https://forgejo.example.test/' --show-only templates/deployment.yaml) ||
  fail "forgejo.baseURL does not reach FORGEJO_BASE_URL"
pass "forgejo.baseURL reaches FORGEJO_BASE_URL"

# --- the install store's volume ----------------------------------------------
#
# $DATA_DIR is only worth pointing at a directory that outlives the pod, so the
# chart renders the claim it is backed by. The pod label AInsel routes plugin pods
# by is checked above, on the pod template.

pvc="$(render --show-only templates/pvc.yaml || true)"
grep -qF 'kind: PersistentVolumeClaim' <<<"$pvc" ||
  fail "the chart renders no PersistentVolumeClaim for the install store: $pvc"
pass "the chart renders a PersistentVolumeClaim for the install store"
grep -qE -- '^[[:space:]]*storage: "1Gi"$' <<<"$pvc" ||
  fail "the claim does not ask for 1Gi by default: $pvc"
pass "the claim asks for 1Gi by default"
grep -qE -- '^[[:space:]]*- ReadWriteOnce$' <<<"$pvc" ||
  fail "the claim is not asked for ReadWriteOnce: $pvc"
pass "the claim is asked for ReadWriteOnce"
# An empty storageClass names no class: rendering it would ask the cluster for a
# StorageClass that does not exist instead of using its default.
if grep -q 'storageClassName' <<<"$pvc"; then
  fail "the claim renders an empty storageClassName: $pvc"
fi
pass "the claim omits an empty storageClass"
grep -qF 'storageClassName: "fast-ssd"' <(render --set 'persistence.storageClass=fast-ssd' --show-only templates/pvc.yaml) ||
  fail "persistence.storageClass does not reach the claim"
pass "persistence.storageClass reaches the claim"

# The Deployment must mount the claim it renders, not a name that drifted from it.
claim_name="$(awk '/^metadata:/{meta=1; next} meta && /^  name: /{print $2; exit}' <<<"$pvc")"
[ -n "$claim_name" ] || fail "could not read the claim name from the render"
claim_ref="$(awk '
  /persistentVolumeClaim:/ { pvc = 1; next }
  pvc && /claimName:/ { print $2; exit }
' <<<"$deployment")"
[ "$claim_ref" = "$claim_name" ] ||
  fail "the Deployment mounts claim '${claim_ref}', the rendered claim is named '${claim_name}'"
pass "the Deployment mounts the claim the chart renders ('${claim_name}')"

# With persistence switched off the claim goes away, and the mount stays: the
# binary always has its $DATA_DIR, it just may not survive a restart.
if grep -q 'kind: PersistentVolumeClaim' <(render --set persistence.enabled=false); then
  fail "persistence.enabled=false still renders the PersistentVolumeClaim"
fi
pass "persistence.enabled=false omits the PersistentVolumeClaim"
if ! grep -qE -- '^[[:space:]]*- name: data$' <(render --set persistence.enabled=false --show-only templates/deployment.yaml); then
  fail "persistence.enabled=false drops the data mount"
fi
pass "persistence.enabled=false keeps /data as an emptyDir"

# The Plugin CR is what AInsel proxies to, so its spec.service.name must be the
# name the Service actually gets -- a rename in either template breaks routing.
# `|| true` keeps a broken template in the failure message rather than exiting
# silently on the first bad render.
service_render="$(render --show-only templates/service.yaml || true)"
service_name="$(awk '/^metadata:/{meta=1; next} meta && /^  name: /{print $2; exit}' <<<"$service_render")"
[ -n "$service_name" ] || fail "could not read the Service name from the render"
service_port="$(awk '/^  ports:/{ports=1; next} ports && /^ +port: /{print $2; exit}' <<<"$service_render")"
[ -n "$service_port" ] || fail "could not read the Service port from the render"

plugin="$(render --show-only templates/plugin.yaml || true)"
plugin_service_name="$(awk '/^  service:/{svc=1; next} svc && /^    name: /{print $2; exit}' <<<"$plugin")"
[ "$plugin_service_name" = "$service_name" ] ||
  fail "Plugin CR references service name '${plugin_service_name}', the Service is named '${service_name}'"
pass "Plugin CR references the rendered Service name '${service_name}'"

plugin_service_port="$(awk '/^  service:/{svc=1; next} svc && /^    port: /{print $2; exit}' <<<"$plugin")"
[ "$plugin_service_port" = "$service_port" ] ||
  fail "Plugin CR references service port '${plugin_service_port}', the Service listens on '${service_port}'"
pass "Plugin CR references the rendered Service port '${service_port}'"

# approvedPermissions is optional on the CR: an empty list must be left out
# rather than rendered as an empty one.
if grep -q 'approvedPermissions' <<<"$plugin"; then
  fail "Plugin CR renders approvedPermissions when the list is empty"
fi
pass "Plugin CR omits empty approvedPermissions"

grep -qF 'approvedPermissions:' <(render --set 'plugin.approvedPermissions={invocations:read}') ||
  fail "Plugin CR drops approvedPermissions when one is set"
pass "Plugin CR renders approvedPermissions when set"

# groupAccess is optional the same way: it names the groups whose background
# identity the plugin may act for, and an empty list is not a grant of nothing.
if grep -q 'groupAccess' <<<"$plugin"; then
  fail "Plugin CR renders groupAccess when the list is empty"
fi
pass "Plugin CR omits empty groupAccess"

grep -qF 'groupAccess:' <(render --set 'plugin.groupAccess={sre}') ||
  fail "Plugin CR drops groupAccess when one is set"
pass "Plugin CR renders groupAccess when set"

# Optional objects stay optional: switching them off removes them entirely.
if grep -q 'kind: Plugin' <(render --set plugin.create=false); then
  fail "plugin.create=false still renders the Plugin resource"
fi
pass "plugin.create=false omits the Plugin resource"

if grep -q 'kind: NetworkPolicy' <(render --set networkPolicy.enabled=false); then
  fail "networkPolicy.enabled=false still renders the NetworkPolicy"
fi
pass "networkPolicy.enabled=false omits the NetworkPolicy"

# The NetworkPolicy must admit exactly the two platform clients that reach a
# plugin pod -- the hub proxy and the event-gateway operator -- each as
# its own podSelector peer, and nothing else, on the service port. A single
# merged peer would AND the two label sets and admit neither.
np="$(render --show-only templates/networkpolicy.yaml || true)"
for peer in 'app.kubernetes.io/component: hub' 'app.kubernetes.io/component: event-gateway-operator'; do
  grep -qF -- "$peer" <<<"$np" ||
    fail "NetworkPolicy does not allow the '${peer}' pod selector"
  pass "NetworkPolicy allows ingress from '${peer}'"
done
peers="$(grep -c -- '- podSelector:' <<<"$np" || true)"
[ "$peers" = "2" ] ||
  fail "NetworkPolicy renders ${peers} podSelector peers, expected the hub and the operator as two of them"
pass "NetworkPolicy renders the two peers as separate podSelector entries"
grep -qE -- "port: ${service_port}" <<<"$np" ||
  fail "NetworkPolicy does not restrict ingress to the service port"
pass "NetworkPolicy restricts ingress to the service port ${service_port}"

# --- (c) service.port is the only place the port comes from ------------------
#
# The container port, the $PORT the binary reads, the Service and the
# NetworkPolicy must all follow .Values.service.port. A chart that hardcoded the
# container port while the Service and the NetworkPolicy moved would send DNAT'd
# traffic to a port nothing listens on: the plugin would serve nothing while
# every object rendered happily. That makes it a test, not a comment.

moved="$(render --set service.port=9090 --show-only templates/deployment.yaml || true)"
grep -qF 'containerPort: 9090' <<<"$moved" ||
  fail "service.port=9090 does not move containerPort: $moved"
pass "service.port=9090 moves containerPort to 9090"
grep -qF 'value: "9090"' <<<"$moved" ||
  fail "service.port=9090 does not move the PORT env: $moved"
pass "service.port=9090 moves the PORT env to \"9090\""
# Checked per form rather than as a bare '8080': the HUB_URL default legitimately
# carries the hub's own port, which is the hub's Service port and not this
# container's. What must never appear is a copy of service.port, in any of the
# three shapes the container can take it.
for hard in 'containerPort: 8080' 'port: 8080' 'value: "8080"'; do
  if grep -qF -- "$hard" <<<"$moved"; then
    fail "service.port=9090 still leaves a hardcoded '${hard}' in the Deployment: $moved"
  fi
done
pass "service.port=9090 leaves no hardcoded 8080 in the Deployment"

svc_moved="$(render --set service.port=9090 --show-only templates/service.yaml || true)"
grep -qE -- '^[[:space:]]+port: 9090$' <<<"$svc_moved" ||
  fail "service.port=9090 does not move the Service port: $svc_moved"
pass "service.port=9090 moves the Service port to 9090"

np_moved="$(render --set service.port=9090 --show-only templates/networkpolicy.yaml || true)"
grep -qE -- '^[[:space:]]+port: 9090$' <<<"$np_moved" ||
  fail "service.port=9090 does not move the NetworkPolicy port: $np_moved"
pass "service.port=9090 moves the NetworkPolicy port to 9090"

# --- (c2) an empty allowFrom would be allow-all: the render must refuse it ---

for bad in 'networkPolicy.allowFrom=null' 'networkPolicy.allowFrom[0]=null'; do
  if out="$(render --set "$bad" 2>&1)"; then
    fail "render with $bad succeeded, expected it to fail"
  fi
  case "$out" in
  *"networkPolicy.allowFrom"*) pass "render with $bad fails instead of allowing every source" ;;
  *) fail "render with $bad failed for another reason: $out" ;;
  esac
done

# --- (d) the chart lints -----------------------------------------------------

if ! lint_out="$(helm lint "$CHART_DIR" --set "image.digest=${DIGEST}" 2>&1)"; then
  fail "helm lint failed: ${lint_out}"
fi
pass "helm lint passes with image.digest set"

printf '\nAll chart render checks passed.\n'
