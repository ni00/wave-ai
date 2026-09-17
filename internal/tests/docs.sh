#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

# Generate outside the checkout so this check never repairs stale files silently.
# Comparing bytes also works in source archives without .git metadata.
docs_tmp=$(mktemp -d)
trap 'rm -rf "$docs_tmp"' EXIT
make docs DOCS_DIR="$docs_tmp"
for file in openapi.zh-CN.json; do
  if ! cmp -s "api/$file" "$docs_tmp/$file"; then
    echo "OpenAPI artifact is stale: $file; run make docs" >&2
    diff -u "api/$file" "$docs_tmp/$file" || true
    exit 1
  fi
done
