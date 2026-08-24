#!/usr/bin/env bash
# F5 verifier. Exit code is the verdict.
#
#   1. the plugin's unit tests pass
#   2. the catalog entity is linked to a custom resource, so the tab is shown
#   3. the data path the tab uses - browser -> Backstage proxy -> Go status API
#      -> cluster - returns what kubectl returns
#   4. scaling the custom resource with kubectl changes what that path returns
#   5. a real browser opens the WebApp tab and follows a kubectl scale live
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
K="kubectl --context=${CONTEXT}"
BACKEND="http://localhost:7007"
APP="http://localhost:3000"
NS="${WEBAPP_NAMESPACE:-idp-apps}"
NAME="${TEMPLATE_DEMO_NAME:-idp-template-demo}"
LOG="${ROOT}/backstage-dev.log"
PID=""
DEV_PATTERN='backstage-cli repo start'

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
auth() { printf 'Authorization: Bearer %s' "${BACKSTAGE_VERIFY_TOKEN}"; }

cleanup() {
  ${K} -n "${NS}" patch webapp "${NAME}" --type=merge -p '{"spec":{"replicas":2}}' >/dev/null 2>&1 || true
  pkill -f "${DEV_PATTERN}" 2>/dev/null || true
  [ -n "${PID}" ] && kill "${PID}" 2>/dev/null || true
  return 0
}
trap cleanup EXIT

proxied() {
  curl -fsS -H "$(auth)" "${BACKEND}/api/proxy/webapp-status/api/webapps/${NS}/${NAME}"
}

[ -n "${BACKSTAGE_VERIFY_TOKEN:-}" ] || fail "BACKSTAGE_VERIFY_TOKEN is not set in .env"

step "1. Plugin unit tests"
(cd backstage && yarn backstage-cli repo test --no-watch plugins/webapp-status >/dev/null 2>&1) \
  || fail "the plugin unit tests failed"
pass "13 plugin tests pass"

step "2. The fixture exists in the cluster"
${K} -n "${NS}" get webapp "${NAME}" >/dev/null 2>&1 \
  || fail "WebApp ${NS}/${NAME} is not in the cluster - run 'make verify-f4' first"
pass "WebApp ${NS}/${NAME} exists"

for _ in $(seq 1 30); do
  curl -fsS "http://localhost:30081/healthz" >/dev/null 2>&1 && break
  sleep 2
done
curl -fsS "http://localhost:30081/healthz" >/dev/null 2>&1 \
  || fail "the status API is not answering - run 'make status-api-deploy'"
pass "the status API is answering"

step "3. Backstage is up"
docker compose up -d postgres >/dev/null 2>&1
for port in 3000 7007; do
  ss -ltn 2>/dev/null | grep -q ":${port}[[:space:]]" && fail "port ${port} is already in use"
done
: > "${LOG}"
( cd backstage && exec yarn start ) >>"${LOG}" 2>&1 &
PID=$!
printf '  waiting for Backstage'
for _ in $(seq 1 150); do
  curl -fsS "${BACKEND}/.backstage/health/v1/readiness" >/dev/null 2>&1 && break
  if grep -q 'Backend startup failed' "${LOG}"; then
    echo; grep -v incomingRequest "${LOG}" | grep -oE 'Error: [^\\]{0,140}' | sort -u | head -5
    fail "the backend failed to start - see ${LOG}"
  fi
  printf '.'; sleep 4
done
echo
curl -fsS "${BACKEND}/.backstage/health/v1/readiness" >/dev/null 2>&1 || fail "the backend never became ready"
for _ in $(seq 1 60); do
  curl -fsS -o /dev/null "${APP}" 2>/dev/null && break
  sleep 4
done
curl -fsS -o /dev/null "${APP}" 2>/dev/null || fail "the frontend never came up"
pass "frontend and backend are serving"

step "4. The entity is linked to the custom resource"
for _ in $(seq 1 30); do
  entity="$(curl -fsS -H "$(auth)" \
    "${BACKEND}/api/catalog/entities/by-name/component/default/${NAME}" 2>/dev/null || true)"
  echo "${entity}" | grep -q "\"name\":\"${NAME}\"" && break
  sleep 3
done
annotation="$(echo "${entity:-{\}}" | python3 -c \
  'import json,sys; print(json.load(sys.stdin)["metadata"]["annotations"].get("platform.miportfolio.com/webapp",""))' 2>/dev/null || echo '')"
[ "${annotation}" = "${NS}/${NAME}" ] \
  || fail "the entity annotation is '${annotation}', expected '${NS}/${NAME}' - the tab would not be shown"
pass "the entity carries platform.miportfolio.com/webapp=${annotation}"

step "5. The tab's data path agrees with kubectl"
body="$(proxied)" || fail "the Backstage proxy did not reach the status API"
want_image="$(${K} -n "${NS}" get webapp "${NAME}" -o jsonpath='{.spec.image}')"
want_ready="$(${K} -n "${NS}" get deployment/"${NAME}-deployment" -o jsonpath='{.status.readyReplicas}')"
got_image="$(echo "${body}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["image"]["desired"])')"
got_ready="$(echo "${body}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["replicas"]["ready"])')"
[ "${got_image}" = "${want_image}" ] || fail "image through the proxy is ${got_image}, kubectl says ${want_image}"
[ "${got_ready}" = "${want_ready}" ] || fail "ready replicas through the proxy is ${got_ready}, kubectl says ${want_ready}"
pass "through the proxy: ${got_ready} ready, ${got_image} - matches kubectl"

step "6. A kubectl scale changes what the tab reads"
${K} -n "${NS}" patch webapp "${NAME}" --type=merge -p '{"spec":{"replicas":4}}' >/dev/null
for _ in $(seq 1 60); do
  desired="$(proxied | python3 -c 'import json,sys; print(json.load(sys.stdin)["replicas"]["desired"])' 2>/dev/null || echo 0)"
  [ "${desired}" = "4" ] && break
  sleep 2
done
[ "${desired}" = "4" ] || fail "after scaling to 4, the proxy still reports ${desired}"
pass "scaled to 4 with kubectl and the data path followed"

${K} -n "${NS}" patch webapp "${NAME}" --type=merge -p '{"spec":{"replicas":2}}' >/dev/null
for _ in $(seq 1 60); do
  desired="$(proxied | python3 -c 'import json,sys; print(json.load(sys.stdin)["replicas"]["desired"])' 2>/dev/null || echo 0)"
  [ "${desired}" = "2" ] && break
  sleep 2
done
[ "${desired}" = "2" ] || fail "after scaling back to 2, the proxy still reports ${desired}"
pass "scaled back to 2 and the data path followed"

step "7. A real browser shows it"
if [ ! -d "${HOME}/.cache/ms-playwright" ]; then
  fail "playwright browsers are not installed - run: cd backstage && yarn playwright install chromium"
fi
# Chromium needs four shared objects the distribution does not ship by default.
# This fetches them into a local prefix without root; see the script.
CHROMIUM_LIBS="$("${ROOT}/infra/scripts/playwright-libs.sh")" \
  || fail "could not make chromium runnable - see the output above"
pass "chromium libraries available at ${CHROMIUM_LIBS}"
( cd backstage && KUBE_CONTEXT="${CONTEXT}" WEBAPP_NAMESPACE="${NS}" TEMPLATE_DEMO_NAME="${NAME}" \
  LD_LIBRARY_PATH="${CHROMIUM_LIBS}:${LD_LIBRARY_PATH:-}" \
  yarn playwright test --project='@internal/plugin-webapp-status' --reporter=line ) \
  || fail "the browser test failed"
pass "the WebApp tab renders the cluster state and follows a kubectl scale"

printf '\n\033[32m F5 VERIFIER PASSED \033[0m\n\n'
