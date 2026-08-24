#!/usr/bin/env bash
# Regenerates the screenshots used in the README and the documentation.
#
# They are generated rather than pasted so they can be refreshed when the UI
# changes, instead of quietly ageing into a picture of software that no longer
# looks like this. Backstage has to be running.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

curl -fsS -o /dev/null http://localhost:3000 2>/dev/null \
  || { echo "Backstage is not running on :3000 - start it with 'make dev'" >&2; exit 1; }

LIBS="$("${ROOT}/infra/scripts/playwright-libs.sh")"

( cd backstage && \
  KUBE_CONTEXT="${KUBE_CONTEXT:-kind-idp-local}" \
  LD_LIBRARY_PATH="${LIBS}:${LD_LIBRARY_PATH:-}" \
  yarn playwright test --config demo/playwright.demo.config.ts demo/screenshots.spec.ts --reporter=line )

ls -la "${ROOT}/docs/assets"/*.png
