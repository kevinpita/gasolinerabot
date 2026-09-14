#!/usr/bin/env bash
set -euo pipefail
engine="${CONTAINER_ENGINE:-docker}"
image="${1:?Pass the built image name}"
version="${2:-${GITHUB_SHA:-dev}}"
test "$("$engine" run --rm --network=none --read-only --tmpfs /tmp "$image" --version)" = "$version"
test "$("$engine" image inspect --format '{{.Config.User}}' "$image")" = '10001:10001'
log="$(mktemp)"
trap 'rm -f "$log"' EXIT
if "$engine" run --rm --network=none --read-only --tmpfs /tmp "$image" > "$log" 2>&1; then
  echo 'The bot must reject missing database configuration.' >&2
  exit 1
fi
grep -q 'PostgreSQL environment variables are required' "$log"
echo 'Image smoke checks passed.'
