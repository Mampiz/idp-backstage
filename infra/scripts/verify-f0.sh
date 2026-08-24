#!/usr/bin/env bash
# F0 verifier. Exit code is the verdict: 0 = phase passes, non-zero = it does not.
# Asserts, against the local kind cluster ONLY:
#   1. the WebApp CRD is installed and Established
#   2. the operator manager Deployment is Available
#   3. the admission webhooks have a CA bundle injected (else every apply fails)
#   4. a real WebApp is accepted, reconciled into Deployment+Service+HPA, and its
#      pods reach Ready with status condition Available=True
#   5. an invalid WebApp (:latest) is REJECTED by the operator's own webhook
set -euo pipefail

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
NS="${SMOKE_NS:-idp-demo}"
NAME="smoke-test"
K="kubectl --context=${CONTEXT}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

step "0. Cluster is the local kind cluster"
kubectl config get-contexts -o name | grep -qx "${CONTEXT}" \
  || fail "context ${CONTEXT} not found - run 'make cluster-up'"
server="$(${K} config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
case "${server}" in
  https://127.0.0.1:*|https://localhost:*|https://0.0.0.0:*) pass "API server is local: ${server}" ;;
  *) fail "refusing to run against non-local API server: ${server}" ;;
esac

step "1. WebApp CRD installed"
${K} get crd webapps.platform.miportfolio.com >/dev/null 2>&1 \
  || fail "CRD webapps.platform.miportfolio.com not found"
est="$(${K} get crd webapps.platform.miportfolio.com \
  -o jsonpath='{.status.conditions[?(@.type=="Established")].status}')"
[ "${est}" = "True" ] || fail "CRD not Established (got '${est}')"
pass "CRD webapps.platform.miportfolio.com is Established"

step "2. Operator Deployment available"
${K} -n webapp-operator-system wait deployment/webapp-operator-controller-manager \
  --for=condition=Available --timeout=180s >/dev/null \
  || fail "operator manager deployment is not Available"
pass "webapp-operator-controller-manager is Available"

step "3. Admission webhooks have a CA bundle (cert-manager injection)"
for wh in validatingwebhookconfiguration/webapp-operator-validating-webhook-configuration \
          mutatingwebhookconfiguration/webapp-operator-mutating-webhook-configuration; do
  ca="$(${K} get "${wh}" -o jsonpath='{.webhooks[0].clientConfig.caBundle}')"
  [ -n "${ca}" ] || fail "${wh} has an empty caBundle - cert-manager did not inject it"
done
pass "both webhook configurations have an injected caBundle"

step "4. A valid WebApp is accepted and reconciled"
# The webhooks fail closed, so an apply issued before they are serving is
# rejected with a connection error. Wait for them to admit requests first; this
# is an extra assertion, not a softer one.
KUBE_CONTEXT="${CONTEXT}" "${ROOT}/infra/scripts/wait-operator-webhook.sh" \
  || fail "the operator webhooks never started serving"
${K} create namespace "${NS}" --dry-run=client -o yaml | ${K} apply -f - >/dev/null
${K} apply -f "${ROOT}/infra/test/webapp-smoke.yaml" >/dev/null \
  || fail "apply of the smoke WebApp was rejected"
pass "WebApp ${NS}/${NAME} applied"

# The operator names the children it owns with a suffix, not with the WebApp
# name verbatim: <webapp>-deployment / -service / -autoscaler. Anything that
# resolves a WebApp to its workload (F2 status-api, F5 plugin) must use this.
${K} -n "${NS}" wait deployment/"${NAME}-deployment" --for=condition=Available --timeout=180s >/dev/null \
  || fail "Deployment ${NAME}-deployment never became Available"
pass "Deployment ${NAME}-deployment is Available"

${K} -n "${NS}" get service/"${NAME}-service" >/dev/null 2>&1 || fail "Service ${NAME}-service was not created"
pass "Service ${NAME}-service exists"
${K} -n "${NS}" get hpa/"${NAME}-autoscaler" >/dev/null 2>&1 || fail "HPA ${NAME}-autoscaler was not created"
pass "HorizontalPodAutoscaler ${NAME}-autoscaler exists"

# condition=Available only guarantees minAvailable, not the full replica count,
# so poll until every desired replica is actually Ready.
desired="$(${K} -n "${NS}" get webapp "${NAME}" -o jsonpath='{.spec.replicas}')"
for _ in $(seq 1 60); do
  ready="$(${K} -n "${NS}" get deployment/"${NAME}-deployment" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
  [ "${ready:-0}" -ge "${desired}" ] && break
  sleep 3
done
[ "${ready:-0}" -ge "${desired}" ] || fail "expected ${desired} ready replicas, got '${ready:-0}'"
pass "${ready}/${desired} pods Ready"

for _ in $(seq 1 30); do
  avail="$(${K} -n "${NS}" get webapp "${NAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)"
  [ "${avail}" = "True" ] && break
  sleep 2
done
[ "${avail:-}" = "True" ] || fail "WebApp status condition Available is '${avail:-<empty>}', expected True"
msg="$(${K} -n "${NS}" get webapp "${NAME}" \
  -o jsonpath='{.status.conditions[?(@.type=="Available")].message}')"
pass "WebApp condition Available=True (\"${msg}\")"

step "5. An invalid WebApp (:latest) is rejected by the operator webhook"
if ${K} apply --dry-run=server -f - >/dev/null 2>&1 <<EOF
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata: {name: must-be-rejected, namespace: ${NS}}
spec: {image: "nginx:latest", replicas: 1, port: 80}
EOF
then
  fail "a WebApp with image nginx:latest was ACCEPTED - the validating webhook is not effective"
fi
pass "validating webhook rejected the :latest image as expected"

printf '\n\033[32m F0 VERIFIER PASSED \033[0m\n\n'
