#!/usr/bin/env bash
# Checks the four open facts of docs/design/2026-10-24-setup-wizard-flow.md §8
# against a dev install, and deletes everything it creates (also on failure).
#
#   V1  plugin-forgejo tools appear in an image's catalog, gated by `enabled`
#   V3  how the tool catalog of a new image gets filled, and how long it takes
#   V8  an admin-created Forgejo user can mint its own scoped token (basic auth)
#   V10 org hooks: create, update, test, and readable delivery status
#
# Needs:
#   AINSEL_HUB     hub base URL, e.g. https://devoper.duckdns.org/ainsel-dev
#   AINSEL_TOKEN   an admin bearer for the hub REST API
#   FORGEJO_URL    e.g. https://devoper.duckdns.org/forgejo
#   FORGEJO_TOKEN  a site-admin token
# Optional:
#   FORGE_MCP      name of an existing forge MCP server (default forgejo-mcp-server)
#   RUNTIME        runtime container (default dpinsel/ainsel-pi:dev)
#   GROUP_ID       hub group for the throwaway image (required if authz is on)
set -uo pipefail
: "${AINSEL_HUB:?}" "${AINSEL_TOKEN:?}" "${FORGEJO_URL:?}" "${FORGEJO_TOKEN:?}"
FORGE_MCP=${FORGE_MCP:-forgejo-mcp-server}
RUNTIME=${RUNTIME:-dpinsel/ainsel-pi:dev}
GROUP_ID=${GROUP_ID:-}
TAG=zz-ainsel-check-$(date +%s)
HUB="$AINSEL_HUB/api/v1"; FJ="$FORGEJO_URL/api/v1"
IMAGE_ID=""; ORG=""; BOT=""

hub() { curl -sS -H "Authorization: Bearer $AINSEL_TOKEN" -H 'Content-Type: application/json' "$@"; }
fj()  { curl -sS -H "Authorization: token $FORGEJO_TOKEN" -H 'Content-Type: application/json' "$@"; }
say() { printf '\n== %s\n' "$*"; }

cleanup() {
  say cleanup
  [ -n "$IMAGE_ID" ] && hub -X DELETE -o /dev/null -w "image delete %{http_code}\n" "$HUB/agent-images/$IMAGE_ID"
  [ -n "$ORG" ] && fj -X DELETE -o /dev/null -w "org delete %{http_code}\n" "$FJ/orgs/$ORG"
  [ -n "$BOT" ] && fj -X DELETE -o /dev/null -w "user delete %{http_code}\n" "$FJ/admin/users/$BOT?purge=true"
}
trap cleanup EXIT

# --- V1 + V3 -----------------------------------------------------------------
say "V1/V3: create image $TAG with mcpServers [$FORGE_MCP, plugin-forgejo]"
START=$(date +%s)
RESP=$(hub -X POST "$HUB/agent-images" -d "{\"displayName\":\"$TAG\",\"groupId\":\"$GROUP_ID\",\"imageURL\":\"$RUNTIME\",\"mcpServers\":[\"$FORGE_MCP\",\"plugin-forgejo\"]}")
IMAGE_ID=$(printf '%s' "$RESP" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
echo "create → id=${IMAGE_ID:-<none>}  ${RESP:0:200}"
if [ -n "$IMAGE_ID" ]; then
  for i in $(seq 1 24); do
    OUT=$(hub "$HUB/agent-images/$IMAGE_ID" | python3 -c '
import json,sys
d=json.load(sys.stdin); t=d.get("tools") or []
src={}
for x in t: src[x.get("mcpSource") or x.get("kind")]=src.get(x.get("mcpSource") or x.get("kind"),0)+1
print(len(t), json.dumps(src), (d.get("status") or {}).get("phase"))')
    echo "t+$(( $(date +%s) - START ))s tools/sources/phase: $OUT"
    case "$OUT" in 0\ *) sleep 5 ;; *) break ;; esac
  done
  echo "V1: is there a plugin-forgejo source above? V3: elapsed time to a filled catalog (or does it stay 0 = tools are declared, not discovered)?"
fi

# --- V8 ----------------------------------------------------------------------
BOT=$TAG-bot; PW="Zz$(openssl rand -hex 12)!a"
say "V8: create user $BOT and mint its token over basic auth"
fj -X POST "$FJ/admin/users" -d "{\"username\":\"$BOT\",\"email\":\"$BOT@ainsel.invalid\",\"password\":\"$PW\",\"must_change_password\":false,\"visibility\":\"private\",\"full_name\":\"AInsel Check\"}" -o /dev/null -w "user create %{http_code}\n"
curl -sS -u "$BOT:$PW" -H 'Content-Type: application/json' -X POST "$FJ/users/$BOT/tokens" \
  -d '{"name":"check","scopes":["write:issue","write:repository","read:user","read:organization"]}' \
  -o /dev/null -w "basic-auth token mint %{http_code} (201 = V8 holds)\n"
fj -H "Sudo: $BOT" -X POST "$FJ/users/$BOT/tokens" -d '{"name":"check-sudo","scopes":["read:user"]}' \
  -o /dev/null -w "admin Sudo token mint %{http_code} (fallback)\n"

# --- V10 ---------------------------------------------------------------------
ORG=$TAG
say "V10: org $ORG, team, org hook"
fj -X POST "$FJ/orgs" -d "{\"username\":\"$ORG\",\"visibility\":\"private\"}" -o /dev/null -w "org create %{http_code}\n"
TEAM=$(fj -X POST "$FJ/orgs/$ORG/teams" -d '{"name":"ainsel-refiner-check","permission":"write","includes_all_repositories":true,"units_map":{"repo.code":"read","repo.issues":"write","repo.pulls":"read"}}' | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))')
echo "team id=$TEAM"; fj -X PUT "$FJ/teams/$TEAM/members/$BOT" -o /dev/null -w "team add member %{http_code}\n"
HOOK=$(fj -X POST "$FJ/orgs/$ORG/hooks" -d '{"type":"forgejo","active":true,"events":["issues","issue_comment","pull_request","pull_request_review"],"config":{"url":"https://example.invalid/webhooks/check","content_type":"json","secret":"check-secret"}}' | python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))')
echo "hook id=$HOOK"
fj -X PATCH "$FJ/orgs/$ORG/hooks/$HOOK" -d '{"config":{"url":"https://example.invalid/webhooks/check2","content_type":"json","secret":"check-secret-2"}}' -o /dev/null -w "hook update %{http_code}\n"
fj -X POST "$FJ/orgs/$ORG/hooks/$HOOK/tests" -o /dev/null -w "org hook test endpoint %{http_code} (404 = none; S7 asks for one real event instead)\n"
echo "hook fields: $(fj "$FJ/orgs/$ORG/hooks/$HOOK" | python3 -c 'import json,sys;print(sorted(json.load(sys.stdin).keys()))')"
echo "(look for a last-delivery/status field; if none, S7 can't read the delivery status through the API)"
