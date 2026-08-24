#!/usr/bin/env bash
# Waits until the operator's admission webhooks are actually serving.
#
# Same trap as cert-manager: the manager Deployment reports Available before its
# webhook Service has endpoints that accept connections, and because the webhook
# configurations use failurePolicy: fail, anything applying a WebApp in that
# window is rejected with
#
#   failed calling webhook "mwebapp-v1.kb.io": ... connection refused
#
# So this asks the webhook to admit a throwaway WebApp with a server-side dry run
# and waits for that to succeed.
set -euo pipefail

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
TIMEOUT="${TIMEOUT:-180}"
K="kubectl --context=${CONTEXT}"

probe() {
  ${K} apply --dry-run=server -f - >/dev/null 2>&1 <<'EOF'
apiVersion: platform.miportfolio.com/v1
kind: WebApp
metadata:
  name: webhook-readiness-probe
  namespace: default
spec:
  image: nginx:1.27-alpine
  replicas: 1
  port: 80
EOF
}

printf 'waiting for the webapp-operator webhooks to admit requests'
deadline=$(( $(date +%s) + TIMEOUT ))
until probe; do
  if [ "$(date +%s)" -ge "${deadline}" ]; then
    echo
    echo "the operator webhooks did not start serving within ${TIMEOUT}s" >&2
    ${K} -n webapp-operator-system get pods,endpoints >&2 || true
    exit 1
  fi
  printf '.'
  sleep 2
done
echo " ready"
