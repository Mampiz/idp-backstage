#!/usr/bin/env bash
# F3 verifier. Exit code is the verdict.
#
# This one creates real things: a real repository in a real GitHub account and a
# real workload in the cluster. It is written to be re-runnable, because every
# step of the service it exercises is idempotent.
#
#   1. the unit tests pass
#   2. a POST to /scaffold creates a repository that really exists on GitHub,
#      with the scaffolded content in it
#   3. the WebApp custom resource is really in the cluster and its pods reach Ready
#   4. re-sending the same request is a no-op, not a failure
#   5. a request the operator would reject is refused before anything is created
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
K="kubectl --context=${CONTEXT}"
BASE="${SCAFFOLDER_URL:-http://localhost:30080}"
OWNER="${GITHUB_OWNER:-Mampiz}"
DEMO="${SCAFFOLD_DEMO_NAME:-idp-scaffold-demo}"
NS="${WEBAPP_NAMESPACE:-idp-apps}"
# A real, pinned, pullable image: the scaffolded repository has not published
# one of its own yet at the moment the custom resource is applied.
IMAGE="${SCAFFOLD_DEMO_IMAGE:-nginx:1.27-alpine}"

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

[ -n "${GITHUB_TOKEN:-}" ] || fail "GITHUB_TOKEN is not exported - run: export GITHUB_TOKEN=\$(gh auth token)"

step "1. Unit tests"
(cd services/scaffolder && go test ./... >/dev/null) || fail "go test failed in services/scaffolder"
pass "go test ./... is clean"

step "2. The service is reachable"
# A rollout leaves a window where the Service has no ready endpoint, so this
# waits rather than treating a redeploy as a failure.
for _ in $(seq 1 30); do
  curl -fsS "${BASE}/healthz" >/dev/null 2>&1 && break
  sleep 2
done
curl -fsS "${BASE}/healthz" >/dev/null 2>&1 \
  || fail "the scaffolder is not answering on ${BASE} - run 'make scaffolder-deploy'"
pass "scaffolder answering on ${BASE}"

payload="$(cat <<EOF
{
  "name": "${DEMO}",
  "owner": "${OWNER}",
  "repoUrl": "github.com?owner=${OWNER}&repo=${DEMO}",
  "description": "Service scaffolded by the IDP verifier.",
  "image": "${IMAGE}",
  "port": 80,
  "replicas": 2
}
EOF
)"

step "3. POST /scaffold"
response="$(curl -sS -w '\n%{http_code}' -X POST "${BASE}/scaffold" \
  -H 'Content-Type: application/json' -d "${payload}")"
code="$(echo "${response}" | tail -1)"
body="$(echo "${response}" | sed '$d')"
case "${code}" in
  201) pass "scaffold returned 201" ;;
  207) fail "provisioning stopped half way: ${body}" ;;
  *)   fail "scaffold returned ${code}: ${body}" ;;
esac

step "4. The repository really exists on GitHub"
gh repo view "${OWNER}/${DEMO}" --json name,url >/dev/null 2>&1 \
  || fail "${OWNER}/${DEMO} is not on GitHub"
pass "${OWNER}/${DEMO} exists"

for path in main.go go.mod Dockerfile Makefile README.md catalog-info.yaml webapp.yaml .github/workflows/ci.yml; do
  gh api "repos/${OWNER}/${DEMO}/contents/${path}" --jq '.name' >/dev/null 2>&1 \
    || fail "the repository is missing ${path}"
done
pass "the scaffolded content is in the repository"

topics="$(gh api "repos/${OWNER}/${DEMO}/topics" --jq '.names | join(",")' 2>/dev/null || echo '')"
case "${topics}" in
  *idp-managed*) pass "the repository is tagged idp-managed" ;;
  *) fail "topics are ${topics:-<none>}, expected idp-managed" ;;
esac

step "5. The custom resource is in the cluster and its pods are Ready"
${K} -n "${NS}" get webapp "${DEMO}" >/dev/null 2>&1 \
  || fail "WebApp ${NS}/${DEMO} is not in the cluster"
pass "kubectl lists WebApp ${NS}/${DEMO}"

${K} -n "${NS}" wait deployment/"${DEMO}-deployment" --for=condition=Available --timeout=180s >/dev/null \
  || fail "the Deployment never became Available"

desired="$(${K} -n "${NS}" get webapp "${DEMO}" -o jsonpath='{.spec.replicas}')"
for _ in $(seq 1 60); do
  ready="$(${K} -n "${NS}" get deployment/"${DEMO}-deployment" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
  [ "${ready:-0}" -ge "${desired}" ] && break
  sleep 3
done
[ "${ready:-0}" -ge "${desired}" ] || fail "expected ${desired} ready pods, got ${ready:-0}"
pass "${ready}/${desired} pods Ready"

for _ in $(seq 1 30); do
  avail="$(${K} -n "${NS}" get webapp "${DEMO}" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)"
  [ "${avail}" = "True" ] && break
  sleep 2
done
[ "${avail:-}" = "True" ] || fail "the WebApp condition Available is ${avail:-<empty>}"
pass "WebApp condition Available=True"

step "6. The same request again is a no-op, not a failure"
again="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${BASE}/scaffold" \
  -H 'Content-Type: application/json' -d "${payload}")"
[ "${again}" = "201" ] || fail "a repeated request returned ${again}, want 201"
pass "the endpoint is idempotent"

step "7. A request the operator would reject is refused up front"
bad="$(curl -sS -w '\n%{http_code}' -X POST "${BASE}/scaffold" -H 'Content-Type: application/json' \
  -d "{\"name\":\"must-not-be-created\",\"owner\":\"${OWNER}\",\"image\":\"nginx:latest\",\"port\":80,\"replicas\":1}")"
badcode="$(echo "${bad}" | tail -1)"
[ "${badcode}" = "400" ] || fail "an image with :latest returned ${badcode}, want 400"
if gh repo view "${OWNER}/must-not-be-created" >/dev/null 2>&1; then
  fail "a repository was created for a request that could never have worked"
fi
pass "rejected before creating anything"

printf '\n\033[32m F3 VERIFIER PASSED \033[0m\n'
printf '  repository: https://github.com/%s/%s\n' "${OWNER}" "${DEMO}"
printf '  workload:   kubectl -n %s get webapp,deploy,pods\n\n' "${NS}"
