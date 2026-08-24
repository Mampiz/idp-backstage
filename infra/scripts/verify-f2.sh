#!/usr/bin/env bash
# F2 verifier. Exit code is the verdict.
#   1. the unit tests pass
#   2. the service, run against the real kind cluster, serves the WebApp custom
#      resources that are actually there, with data that matches kubectl
#   3. it reports a WebApp that does not exist as 404, and readiness as ready
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
K="kubectl --context=${CONTEXT}"
BASE="http://localhost:8081"
NS="idp-demo"
NAME="smoke-test"
PID=""

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

cleanup() {
  [ -n "${PID}" ] && kill "${PID}" 2>/dev/null || true
  return 0
}
trap cleanup EXIT

step "1. Unit tests"
(cd services/status-api && go test ./... >/dev/null) || fail "go test failed in services/status-api"
pass "go test ./... is clean"

step "2. A WebApp exists in the cluster to report on"
${K} get crd webapps.platform.miportfolio.com >/dev/null 2>&1 || fail "the CRD is missing - run 'make bootstrap'"
${K} create namespace "${NS}" --dry-run=client -o yaml | ${K} apply -f - >/dev/null
${K} apply -f infra/test/webapp-smoke.yaml >/dev/null
${K} -n "${NS}" wait deployment/"${NAME}-deployment" --for=condition=Available --timeout=180s >/dev/null \
  || fail "the smoke WebApp never became Available"
pass "WebApp ${NS}/${NAME} is running in the cluster"

step "3. The service serves what is really there"
ss -ltn 2>/dev/null | grep -q ':8081[[:space:]]' && fail "port 8081 is already in use"
KUBE_CONTEXT="${CONTEXT}" go run ./services/status-api/cmd/status-api >/tmp/status-api-verify.log 2>&1 &
PID=$!

for _ in $(seq 1 60); do
  curl -fsS "${BASE}/readyz" >/dev/null 2>&1 && break
  kill -0 "${PID}" 2>/dev/null || { tail -20 /tmp/status-api-verify.log; fail "the service exited during startup"; }
  sleep 1
done
curl -fsS "${BASE}/readyz" >/dev/null 2>&1 || { tail -20 /tmp/status-api-verify.log; fail "/readyz never reported ready"; }
pass "/readyz is ready once the informer caches are synced"

body="$(curl -fsS "${BASE}/api/webapps")" || fail "GET /api/webapps failed"
echo "${body}" | grep -q "\"name\":\"${NAME}\"" || fail "the collection does not contain ${NAME}: ${body}"
pass "GET /api/webapps lists ${NAME}"

one="$(curl -fsS "${BASE}/api/webapps/${NS}/${NAME}")" || fail "GET /api/webapps/${NS}/${NAME} failed"

# The API is only useful if it matches the cluster, so compare against kubectl
# rather than against itself.
want_ready="$(${K} -n "${NS}" get deployment/"${NAME}-deployment" -o jsonpath='{.status.readyReplicas}')"
want_image="$(${K} -n "${NS}" get webapp "${NAME}" -o jsonpath='{.spec.image}')"
want_avail="$(${K} -n "${NS}" get webapp "${NAME}" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"

got_ready="$(echo "${one}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["replicas"]["ready"])')"
got_image="$(echo "${one}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["image"]["desired"])')"
got_deployed="$(echo "${one}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["image"]["deployed"])')"
got_avail="$(echo "${one}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["condition"]["status"])')"
got_deploy="$(echo "${one}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["deploymentName"])')"

[ "${got_ready}" = "${want_ready}" ] || fail "ready replicas: API says ${got_ready}, cluster says ${want_ready}"
[ "${got_image}" = "${want_image}" ] || fail "image: API says ${got_image}, cluster says ${want_image}"
[ "${got_deployed}" = "${want_image}" ] || fail "deployed image: API says ${got_deployed}, cluster says ${want_image}"
[ "${got_avail}" = "${want_avail}" ] || fail "Available: API says ${got_avail}, cluster says ${want_avail}"
[ "${got_deploy}" = "${NAME}-deployment" ] || fail "deployment name: API says ${got_deploy}"
pass "replicas, image and condition all match kubectl (${got_ready} ready, ${got_image}, Available=${got_avail})"

step "4. Missing resources and metrics"
code="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/api/webapps/${NS}/does-not-exist")"
[ "${code}" = "404" ] || fail "a missing WebApp returned ${code}, want 404"
pass "a missing WebApp is a 404"

curl -fsS "${BASE}/metrics" | grep -q 'status_api_webapps' || fail "/metrics does not expose the cache gauges"
pass "/metrics exposes the cache gauges"

printf '\n\033[32m F2 VERIFIER PASSED \033[0m\n\n'
