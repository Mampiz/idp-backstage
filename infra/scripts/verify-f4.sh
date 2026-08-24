#!/usr/bin/env bash
# F4 verifier. Exit code is the verdict.
#
# The brief's verifier is "fill in the form at /create and a repository, a custom
# resource and pods come out, with nothing done by hand". Filling a form cannot
# be scripted, but the form is only a client of the scaffolder API: submitting it
# executes the template through POST /api/scaffolder/v2/tasks. This drives that
# exact path with the exact values the form would send, so what is proven is the
# same thing, minus the mouse.
#
#   1. the template is in the catalog and its parameters are what the form needs
#   2. executing it succeeds
#   3. the repository, the custom resource and the pods all really exist
#   4. the component ends up in the catalog
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
K="kubectl --context=${CONTEXT}"
BACKEND="http://localhost:7007"
SCAFFOLDER="${SCAFFOLDER_URL:-http://localhost:30080}"
OWNER="${GITHUB_OWNER:-Mampiz}"
DEMO="${TEMPLATE_DEMO_NAME:-idp-template-demo}"
NS="${WEBAPP_NAMESPACE:-idp-apps}"
IMAGE="${TEMPLATE_DEMO_IMAGE:-nginx:1.27-alpine}"
LOG="${ROOT}/backstage-dev.log"
PID=""
DEV_PATTERN='backstage-cli package start'

# kubectl wait errors out immediately when the resource does not exist yet,
# rather than waiting for it to appear. The operator creates the Deployment a
# moment after the custom resource is admitted, so wait for it to exist first.
wait_for_deployment() {
  local namespace="$1" name="$2"
  for _ in $(seq 1 60); do
    ${K} -n "${namespace}" get deployment "${name}" >/dev/null 2>&1 && return 0
    sleep 2
  done
  return 1
}

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
auth()  { printf 'Authorization: Bearer %s' "${BACKSTAGE_VERIFY_TOKEN}"; }

cleanup() {
  pkill -f "${DEV_PATTERN}" 2>/dev/null || true
  [ -n "${PID}" ] && kill "${PID}" 2>/dev/null || true
  return 0
}
trap cleanup EXIT

[ -n "${GITHUB_TOKEN:-}" ] || fail "GITHUB_TOKEN is not exported"
[ -n "${BACKSTAGE_VERIFY_TOKEN:-}" ] || fail "BACKSTAGE_VERIFY_TOKEN is not set in .env"

step "1. Dependencies are up"
docker compose up -d postgres >/dev/null 2>&1
for _ in $(seq 1 30); do
  docker compose exec -T postgres pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null 2>&1 && break
  sleep 1
done
pass "postgres is accepting connections"

for _ in $(seq 1 30); do
  curl -fsS "${SCAFFOLDER}/healthz" >/dev/null 2>&1 && break
  sleep 2
done
curl -fsS "${SCAFFOLDER}/healthz" >/dev/null 2>&1 \
  || fail "the Go scaffolder is not answering on ${SCAFFOLDER} - run 'make scaffolder-deploy'"
pass "the Go scaffolder is answering"

step "2. Backstage backend boots"
ss -ltn 2>/dev/null | grep -q ':7007[[:space:]]' && fail "port 7007 is already in use"
: > "${LOG}"
( cd backstage && exec yarn workspace backend start ) >>"${LOG}" 2>&1 &
PID=$!
printf '  waiting for the backend'
for _ in $(seq 1 150); do
  curl -fsS "${BACKEND}/.backstage/health/v1/readiness" >/dev/null 2>&1 && break
  if grep -q 'Backend startup failed' "${LOG}"; then
    echo; grep -v incomingRequest "${LOG}" | grep -oE 'Error: [^\\]{0,140}' | sort -u | head -5
    fail "the backend failed to start - see ${LOG}"
  fi
  printf '.'; sleep 4
done
echo
curl -fsS "${BACKEND}/.backstage/health/v1/readiness" >/dev/null 2>&1 \
  || { grep -v incomingRequest "${LOG}" | tail -20; fail "the backend never became ready"; }
pass "backend ready"

step "3. The template is registered with the parameters the form needs"
for _ in $(seq 1 30); do
  template="$(curl -fsS -H "$(auth)" \
    "${BACKEND}/api/catalog/entities/by-name/template/default/webapp-service" 2>/dev/null || true)"
  echo "${template}" | grep -q '"name":"webapp-service"' && break
  sleep 3
done
echo "${template:-}" | grep -q '"name":"webapp-service"' \
  || fail "template/default/webapp-service is not in the catalog"
pass "the template is in the catalog"

python3 - "${template}" <<'PY' || exit 1
import json, sys
spec = json.loads(sys.argv[1])["spec"]
props = {}
for page in spec["parameters"]:
    props.update(page.get("properties", {}))
missing = [f for f in ("name", "description", "owner", "repoUrl", "image", "port", "replicas") if f not in props]
if missing:
    print(f"  \033[31mFAIL\033[0m  the form is missing fields: {missing}", file=sys.stderr)
    sys.exit(1)
if props["owner"].get("ui:field") != "OwnerPicker":
    print("  \033[31mFAIL\033[0m  owner does not use the OwnerPicker", file=sys.stderr)
    sys.exit(1)
if props["repoUrl"].get("ui:field") != "RepoUrlPicker":
    print("  \033[31mFAIL\033[0m  repoUrl does not use the RepoUrlPicker", file=sys.stderr)
    sys.exit(1)
print("  \033[32mPASS\033[0m  every field is present, with the OwnerPicker and the RepoUrlPicker")
PY

# A hyphen inside ${{ steps.x.output.y }} is parsed as subtraction and silently
# yields NaN, so step ids must never contain one.
if grep -qE '^\s+- id: [a-zA-Z0-9]*-' templates/webapp-service/template.yaml; then
  fail "a step id contains a hyphen; it will be evaluated as subtraction inside \${{ }}"
fi
pass "no step id contains a hyphen"

step "4. Executing the template does the whole job"
task="$(curl -fsS -X POST "${BACKEND}/api/scaffolder/v2/tasks" \
  -H "$(auth)" -H 'Content-Type: application/json' \
  -d "{\"templateRef\":\"template:default/webapp-service\",\"values\":{
        \"name\":\"${DEMO}\",
        \"description\":\"Created by the F4 verifier.\",
        \"owner\":\"group:default/platform\",
        \"repoUrl\":\"github.com?owner=${OWNER}&repo=${DEMO}\",
        \"image\":\"${IMAGE}\",
        \"port\":80,
        \"replicas\":2}}")" || fail "could not create the scaffolder task"

taskId="$(echo "${task}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
pass "task ${taskId} created"

printf '  waiting for the task'
for _ in $(seq 1 90); do
  status="$(curl -fsS -H "$(auth)" "${BACKEND}/api/scaffolder/v2/tasks/${taskId}" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')"
  case "${status}" in
    completed|failed|cancelled) break ;;
  esac
  printf '.'; sleep 2
done
echo
if [ "${status}" != "completed" ]; then
  curl -fsS -H "$(auth)" "${BACKEND}/api/scaffolder/v2/tasks/${taskId}/eventstream" --max-time 5 2>/dev/null | tail -20 || true
  fail "the task finished as ${status}"
fi
pass "the task completed"

step "5. The repository, the custom resource and the pods all exist"
gh repo view "${OWNER}/${DEMO}" --json name >/dev/null 2>&1 || fail "${OWNER}/${DEMO} is not on GitHub"
pass "repository ${OWNER}/${DEMO} exists"

${K} -n "${NS}" get webapp "${DEMO}" >/dev/null 2>&1 || fail "WebApp ${NS}/${DEMO} is not in the cluster"
wait_for_deployment "${NS}" "${DEMO}-deployment" \
  || fail "the operator never created Deployment ${DEMO}-deployment"
${K} -n "${NS}" wait deployment/"${DEMO}-deployment" --for=condition=Available --timeout=180s >/dev/null \
  || fail "the Deployment never became Available"
desired="$(${K} -n "${NS}" get webapp "${DEMO}" -o jsonpath='{.spec.replicas}')"
for _ in $(seq 1 60); do
  ready="$(${K} -n "${NS}" get deployment/"${DEMO}-deployment" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
  [ "${ready:-0}" -ge "${desired}" ] && break
  sleep 3
done
[ "${ready:-0}" -ge "${desired}" ] || fail "expected ${desired} ready pods, got ${ready:-0}"
pass "WebApp ${NS}/${DEMO} running ${ready}/${desired} pods"

step "6. The component is in the catalog"
for _ in $(seq 1 30); do
  entity="$(curl -fsS -H "$(auth)" \
    "${BACKEND}/api/catalog/entities/by-name/component/default/${DEMO}" 2>/dev/null || true)"
  echo "${entity}" | grep -q "\"name\":\"${DEMO}\"" && break
  sleep 3
done
echo "${entity:-}" | grep -q "\"name\":\"${DEMO}\"" \
  || fail "component/default/${DEMO} never appeared in the catalog"
pass "component ${DEMO} is in the catalog"

printf '\n\033[32m F4 VERIFIER PASSED \033[0m\n'
printf '  repository: https://github.com/%s/%s\n\n' "${OWNER}" "${DEMO}"
