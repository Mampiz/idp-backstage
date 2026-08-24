#!/usr/bin/env bash
# Records the README demo: drives the portal in a real browser, then turns the
# video into a GIF.
#
# It is not a verifier and nothing depends on it. It does create a real
# repository and a real workload, because a demo of a platform that fakes the
# platform is worth nothing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
[ -f .env ] && set -a && . ./.env && set +a

CONTEXT="${KUBE_CONTEXT:-kind-idp-local}"
OUT="${ROOT}/docs/assets/demo.gif"
RECORDING="${ROOT}/backstage/demo/recording"
# Playwright ships an ffmpeg, but it is a minimal build whose only video filter
# is scale - no fps, no setpts, no palettegen - so it cannot produce a decent
# GIF. A full ffmpeg runs in a container instead, which also means this script
# needs nothing installed beyond what the rest of the repository already needs.
FFMPEG_IMAGE="${FFMPEG_IMAGE:-jrottenberg/ffmpeg:6-alpine}"

echo "==> Making chromium runnable"
LIBS="$("${ROOT}/infra/scripts/playwright-libs.sh")"

echo "==> Recording"
rm -rf "${RECORDING}"
( cd backstage && \
  KUBE_CONTEXT="${CONTEXT}" GITHUB_OWNER="${GITHUB_OWNER:-Mampiz}" \
  LD_LIBRARY_PATH="${LIBS}:${LD_LIBRARY_PATH:-}" \
  yarn playwright test --config demo/playwright.demo.config.ts )

video="$(find "${RECORDING}" -name '*.webm' | head -1)"
[ -n "${video}" ] || { echo "no video was produced" >&2; exit 1; }
echo "==> Recorded $(du -h "${video}" | cut -f1) of video"

mkdir -p "$(dirname "${OUT}")"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
cp "${video}" "${work}/in.webm"

# Sped up and downscaled: a faithful real-time GIF of this flow would be tens of
# megabytes and nobody would watch it to the end. A generated palette keeps the
# UI legible at 10 fps.
filters="setpts=${DEMO_SPEED:-0.5}*PTS,fps=10,scale=${DEMO_WIDTH:-960}:-1:flags=lanczos"

echo "==> Converting with ${FFMPEG_IMAGE}"
docker run --rm -v "${work}":/w -w /w "${FFMPEG_IMAGE}" \
  -y -i in.webm -vf "${filters},palettegen=stats_mode=diff" palette.png >/dev/null 2>&1
docker run --rm -v "${work}":/w -w /w "${FFMPEG_IMAGE}" \
  -y -i in.webm -i palette.png \
  -lavfi "${filters}[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=3" out.gif >/dev/null 2>&1

[ -s "${work}/out.gif" ] || { echo "the conversion produced nothing" >&2; exit 1; }
cp "${work}/out.gif" "${OUT}"

echo "==> Wrote ${OUT} ($(du -h "${OUT}" | cut -f1))"
