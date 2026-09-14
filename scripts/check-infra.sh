#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
helm lint infra/chart
helm template gasolinerabot infra/chart --namespace gasolinerabot > "$tmp/default.yaml"
if grep -q '^kind: Deployment' "$tmp/default.yaml"; then
  echo 'Default chart must not start the unfinished bot.' >&2
  exit 1
fi
helm template gasolinerabot infra/chart --namespace gasolinerabot \
  --set deployment.enabled=true --set image.tag=test > "$tmp/deployment.yaml"
grep -q 'name: TELEGRAM_BOT_TOKEN' "$tmp/deployment.yaml"
grep -q 'type: Recreate' "$tmp/deployment.yaml"
helm template gasolinerabot infra/chart --namespace gasolinerabot \
  --set deployment.enabled=true --set openbao.enabled=true \
  --set health.enabled=true --set image.digest=sha256:test > "$tmp/full.yaml"
grep -q 'key: apps/gasolinerabot' "$tmp/full.yaml"
grep -q '/readyz' "$tmp/full.yaml"
grep -q 'ghcr.io/kevinpita/gasolinerabot@sha256:test' "$tmp/full.yaml"
if helm template gasolinerabot infra/chart --namespace wrong \
  --set openbao.enabled=true > "$tmp/wrong.yaml" 2> "$tmp/error"; then
  echo 'OpenBao must reject the wrong namespace.' >&2
  exit 1
fi
grep -q 'OpenBao access is bound to namespace gasolinerabot' "$tmp/error"
echo 'Deployment checks passed.'
