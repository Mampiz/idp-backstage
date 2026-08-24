#!/usr/bin/env bash
# F6 verifier. Exit code is the verdict.
#
#   1. the IDP's documentation builds with the same mkdocs the portal uses
#   2. the documentation a scaffolded service ships with builds too
#   3. the portal actually serves the built docs for its own component
#   4. the README carries the diagram and the demo, and the demo is a real GIF
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

BACKEND="http://localhost:7007"
TECHDOCS_IMAGE="${TECHDOCS_IMAGE:-spotify/techdocs}"
GIF="docs/assets/demo.gif"

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
auth() { printf 'Authorization: Bearer %s' "${BACKSTAGE_VERIFY_TOKEN}"; }

step "1. The platform's own documentation builds"
docker run --rm -v "${ROOT}":/content -w /content --entrypoint mkdocs \
  "${TECHDOCS_IMAGE}" build -d /tmp/techdocs-verify >/dev/null 2>&1 \
  || fail "mkdocs could not build the site at the repository root"
pass "mkdocs builds the IDP site"

step "2. A scaffolded service ships documentation that builds"
rendered="$(mktemp -d)"
trap 'rm -rf "${rendered}"' EXIT
(cd services/scaffolder && go run ./cmd/render-template "${rendered}" >/dev/null) \
  || fail "could not render the service template"
[ -f "${rendered}/mkdocs.yml" ] || fail "the template does not produce an mkdocs.yml"
docker run --rm -v "${rendered}":/content -w /content --entrypoint mkdocs \
  "${TECHDOCS_IMAGE}" build -d /tmp/techdocs-verify-service >/dev/null 2>&1 \
  || fail "mkdocs could not build the docs the template produces"
pass "the scaffolded service's docs build too"

step "3. The portal serves the built docs"
curl -fsS -o /dev/null "${BACKEND}/.backstage/health/v1/readiness" 2>/dev/null \
  || fail "Backstage is not running - start it with 'make dev'"

entity="$(curl -fsS -H "$(auth)" \
  "${BACKEND}/api/catalog/entities/by-name/component/default/idp-backstage" 2>/dev/null || true)"
echo "${entity}" | grep -q 'techdocs-ref' \
  || fail "the idp-backstage entity has no backstage.io/techdocs-ref annotation"
pass "the entity carries a techdocs-ref annotation"

# The local builder generates on demand; the sync endpoint streams until it is done.
curl -fsS -N --max-time 300 -H "$(auth)" \
  "${BACKEND}/api/techdocs/sync/default/component/idp-backstage" >/tmp/techdocs-sync.log 2>&1 || true
grep -q 'event: error' /tmp/techdocs-sync.log \
  && { tail -3 /tmp/techdocs-sync.log; fail "the portal could not build the docs"; }

code="$(curl -s -o /tmp/techdocs-index.html -w '%{http_code}' -H "$(auth)" \
  "${BACKEND}/api/techdocs/static/docs/default/component/idp-backstage/index.html")"
[ "${code}" = "200" ] || fail "the portal returned ${code} for the docs index"
grep -qi 'Internal Developer Platform' /tmp/techdocs-index.html \
  || fail "the served page is not the documentation"
pass "the portal serves the documentation it built"

step "4. The screenshots the documentation references exist"
for shot in catalog template-form webapp-tab techdocs; do
  file="docs/assets/${shot}.png"
  [ -f "${file}" ] || fail "${file} is missing - regenerate with 'make screenshots'"
  head -c 4 "${file}" | grep -q 'PNG' || fail "${file} is not a PNG"
done
pass "all four screenshots present"

step "5. The README carries the diagram and the demo"
grep -q '```mermaid' README.md || fail "the README has no architecture diagram"
grep -q "${GIF}" README.md || fail "the README does not reference ${GIF}"
[ -f "${GIF}" ] || fail "${GIF} is missing"
head -c 6 "${GIF}" | grep -q 'GIF89a\|GIF87a' || fail "${GIF} is not a GIF"
size="$(stat -c%s "${GIF}")"
[ "${size}" -gt 100000 ] || fail "${GIF} is suspiciously small (${size} bytes)"
[ "${size}" -lt 10485760 ] || fail "${GIF} is ${size} bytes; keep it under 10 MB"
pass "diagram present, demo is a $(( size / 1024 / 1024 )) MB GIF"

printf '\n\033[32m F6 VERIFIER PASSED \033[0m\n\n'
