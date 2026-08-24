#!/usr/bin/env bash
# Makes Playwright's bundled chromium runnable without root.
#
# "playwright install-deps" shells out to apt as root, which is not available
# here and is a heavy ask for a phase verifier. Chromium only actually misses
# four shared objects (libnspr4, libnss3, libnssutil3, libasound), so this
# downloads those three packages with apt-get download - no root needed, it just
# fetches .deb files - unpacks them into a local prefix under node_modules, and
# prints the LD_LIBRARY_PATH to use.
#
# Everything it writes lives under node_modules/.cache, so it is disposable and
# already ignored by git.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFIX="${ROOT}/backstage/node_modules/.cache/playwright-libs"
LIBS="${PREFIX}/usr/lib/x86_64-linux-gnu"
PACKAGES=(libnspr4 libnss3)

# The ALSA package was renamed for the 64-bit time_t transition; take whichever
# the distribution has.
if apt-cache show libasound2t64 >/dev/null 2>&1; then
  PACKAGES+=(libasound2t64)
else
  PACKAGES+=(libasound2)
fi

if [ -f "${LIBS}/libnss3.so" ] && [ -f "${LIBS}/libnspr4.so" ]; then
  echo "${LIBS}"
  exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

(cd "${work}" && apt-get download "${PACKAGES[@]}" >/dev/null 2>&1) || {
  echo "could not download ${PACKAGES[*]}; run 'sudo yarn playwright install-deps chromium' instead" >&2
  exit 1
}

mkdir -p "${PREFIX}"
for deb in "${work}"/*.deb; do
  dpkg-deb -x "${deb}" "${PREFIX}"
done

[ -f "${LIBS}/libnss3.so" ] || { echo "the packages did not contain the expected libraries" >&2; exit 1; }
echo "${LIBS}"
