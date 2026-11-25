#!/usr/bin/env bash
# Merge the per-architecture fsearch binaries into resources/fsearch and
# sign the result ad hoc so it can be run directly from the command line.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
arm="$here/darwin-arm64/fsearch"
intel="$here/darwin-amd64/fsearch"
out="$here/fsearch"

for f in "$arm" "$intel"; do
  if [[ ! -f "$f" ]]; then
    echo "missing: $f" >&2
    echo "put arm64 fsearch in resources/darwin-arm64/ and x86_64 fsearch in resources/darwin-amd64/" >&2
    exit 1
  fi
done

echo "Merging:"
echo "  arm64:  $arm"
echo "  x86_64: $intel"
lipo -create "$arm" "$intel" -output "$out"
chmod +x "$out"
codesign -s - --force "$out"

echo "Result:"
lipo -info "$out"
codesign -vv "$out" 2>&1 | head -2
"$out" version
