#!/usr/bin/env bash
# F1 verifier. Exit code is the verdict.
#   1. Backstage boots against Postgres (not SQLite) and both ports answer
#   2. the catalog serves the webapp-operator Component through the API
#   3. the data is really in the Postgres volume: Backstage is stopped, the
#      container is restarted, and the entity is read straight out of the DB
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

BACKEND_URL="http://localhost:7007"
DISCOVERY_URL="http://localhost:8082"
APP_URL="http://localhost:3000"
LOG="${ROOT}/backstage-dev.log"
PID=""

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

# yarn start spawns a tree of processes (yarn -> backstage-cli -> app + backend).
# Killing only the direct child leaves the dev servers holding ports 3000/7007
# and poisons the next run, so the whole tree is matched by command line.
DEV_PATTERN='backstage-cli repo start'
cleanup() {
  pkill -f "${DEV_PATTERN}" 2>/dev/null || true
  [ -n "${PID}" ] && kill "${PID}" 2>/dev/null || true
  for _ in $(seq 1 20); do pgrep -f "${DEV_PATTERN}" >/dev/null 2>&1 || break; sleep 1; done
  pkill -9 -f "${DEV_PATTERN}" 2>/dev/null || true
  return 0
}
trap cleanup EXIT

port_busy() { ss -ltn 2>/dev/null | grep -q ":$1[[:space:]]"; }

[ -n "${BACKSTAGE_VERIFY_TOKEN:-}" ] || fail "BACKSTAGE_VERIFY_TOKEN is not set - copy .env.example to .env and fill it in"
# The GitHub token is never stored in .env; it has to come from the environment.
[ -n "${GITHUB_TOKEN:-}" ] || fail "GITHUB_TOKEN is not exported - run: export GITHUB_TOKEN=\$(gh auth token)"

step "1. Postgres and the discovery service are up"
docker compose up -d postgres scaffolder >/dev/null 2>&1
for _ in $(seq 1 30); do
  docker compose exec -T postgres pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null 2>&1 && break
  sleep 1
done
docker compose exec -T postgres pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null 2>&1 \
  || fail "postgres never became ready"
pass "postgres accepting connections"

for _ in $(seq 1 30); do
  curl -fsS "${DISCOVERY_URL}/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "${DISCOVERY_URL}/healthz" >/dev/null 2>&1 || fail "the scaffolder service is not answering on ${DISCOVERY_URL}"
discovery="$(curl -fsS "${DISCOVERY_URL}/catalog/discovery")"
echo "${discovery}" | grep -q 'kind: Location' || fail "the discovery endpoint did not return a Location entity"
pass "discovery service serving a Location entity"

step "2. Backstage boots"
for port in 3000 7007; do
  port_busy "${port}" && fail "port ${port} is already in use - stop whatever is holding it first"
done
pass "ports 3000 and 7007 are free"
: > "${LOG}"
( cd backstage && exec yarn start ) >>"${LOG}" 2>&1 &
PID=$!
printf '  waiting for the backend (this takes a while on a cold start)'
for _ in $(seq 1 150); do
  curl -fsS "${BACKEND_URL}/.backstage/health/v1/readiness" >/dev/null 2>&1 && break
  if grep -q 'Backend startup failed' "${LOG}"; then
    echo
    grep -v incomingRequest "${LOG}" | grep -oE 'Error: [^\\]{0,140}' | sort -u | head -5
    fail "backend startup failed - see ${LOG}"
  fi
  kill -0 "${PID}" 2>/dev/null || { echo; grep -v incomingRequest "${LOG}" | tail -20; fail "backstage exited during startup - see ${LOG}"; }
  printf '.'; sleep 4
done
echo
curl -fsS "${BACKEND_URL}/.backstage/health/v1/readiness" >/dev/null 2>&1 \
  || { grep -v incomingRequest "${LOG}" | tail -20; fail "backend never became ready - see ${LOG}"; }
pass "backend ready on ${BACKEND_URL}"

for _ in $(seq 1 60); do
  curl -fsS -o /dev/null "${APP_URL}" 2>/dev/null && break
  sleep 4
done
curl -fsS -o /dev/null "${APP_URL}" 2>/dev/null || fail "frontend never came up on ${APP_URL}"
pass "frontend serving on ${APP_URL}"

step "3. It is running on Postgres, not SQLite"
grep -qi 'better-sqlite3\|sqlite' "${LOG}" && fail "backstage is using SQLite - the whole point is that it is not"
tables="$(docker compose exec -T postgres psql -U "${POSTGRES_USER}" -d backstage_plugin_catalog -tAc \
  "select count(*) from information_schema.tables where table_schema='public'" 2>/dev/null || echo 0)"
[ "${tables:-0}" -gt 0 ] || fail "the catalog plugin created no tables in Postgres"
pass "catalog plugin owns ${tables} tables in Postgres"

step "4. The catalog serves webapp-operator"
for _ in $(seq 1 30); do
  body="$(curl -fsS -H "Authorization: Bearer ${BACKSTAGE_VERIFY_TOKEN}" \
    "${BACKEND_URL}/api/catalog/entities/by-name/component/default/webapp-operator" 2>/dev/null || true)"
  echo "${body}" | grep -q '"name":"webapp-operator"' && break
  sleep 3
done
echo "${body:-}" | grep -q '"name":"webapp-operator"' \
  || fail "component/default/webapp-operator is not in the catalog"
slug="$(echo "${body}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["metadata"]["annotations"]["github.com/project-slug"])')"
[ "${slug}" = "Mampiz/webapp-operator" ] || fail "unexpected project-slug '${slug}'"
pass "Component webapp-operator served by the catalog API (slug ${slug})"

step "5. GitHub discovery reaches the catalog"
# idp-backstage is discovered, not statically registered: it only appears if the
# Go service found its catalog-info.yaml on GitHub and Backstage followed the
# Location entity to it.
for _ in $(seq 1 40); do
  disc_body="$(curl -fsS -H "Authorization: Bearer ${BACKSTAGE_VERIFY_TOKEN}" \
    "${BACKEND_URL}/api/catalog/entities/by-name/component/default/idp-backstage" 2>/dev/null || true)"
  echo "${disc_body}" | grep -q '"name":"idp-backstage"' && break
  sleep 3
done
echo "${disc_body:-}" | grep -q '"name":"idp-backstage"' \
  || fail "component/default/idp-backstage was not discovered - the Location entity did not reach the catalog"
origin="$(echo "${disc_body}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["metadata"]["annotations"]["backstage.io/managed-by-origin-location"])')"
case "${origin}" in
  *localhost:8082*) pass "idp-backstage came in through the Go discovery service (${origin})" ;;
  *) fail "idp-backstage was ingested from ${origin}, not from the discovery service" ;;
esac

step "6. Data survives a restart of the database container"
cleanup; PID=""
sleep 2
docker compose restart postgres >/dev/null 2>&1 || fail "could not restart the postgres container"
for _ in $(seq 1 30); do
  docker compose exec -T postgres pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null 2>&1 && break
  sleep 1
done
pass "postgres container restarted"

# Backstage is stopped, so nothing can re-ingest. If the row is still there it
# came out of the volume, which is the actual claim being tested.
found="$(docker compose exec -T postgres psql -U "${POSTGRES_USER}" -d backstage_plugin_catalog -tAc \
  "select count(*) from final_entities where final_entity::text like '%webapp-operator%'" 2>/dev/null | tr -d '[:space:]')"
[ "${found:-0}" -ge 1 ] || fail "webapp-operator is not in Postgres after the restart (found='${found:-}') - data did not persist"
pass "webapp-operator still stored in Postgres with Backstage shut down"

printf '\n\033[32m F1 VERIFIER PASSED \033[0m\n\n'
