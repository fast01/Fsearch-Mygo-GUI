#!/usr/bin/env bash
# One-shot build of the macOS app: arm64 and universal (Intel + Apple Silicon).
# Requires the per-architecture fsearch binaries in resources/darwin-*/.
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"
cd "$root"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This app can only be built on macOS." >&2
  exit 1
fi

for cmd in go lipo codesign; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing command: $cmd" >&2
    exit 1
  fi
done

for f in resources/darwin-arm64/fsearch resources/darwin-amd64/fsearch; do
  if [[ ! -f "$f" ]]; then
    echo "missing: $f" >&2
    echo "Download/build fsearch v0.2.2 for arm64 and x86_64 first." >&2
    exit 1
  fi
done

# Use the locally installed Go (1.27.3) only; never auto-download a toolchain.
export GOTOOLCHAIN=local

# Go 1.27's linker hardcodes LC_BUILD_VERSION minos 13.0 (macOS 13 Ventura),
# which makes macOS 12.x refuse to launch the app. MyGo passes MYGO_LDFLAGS
# through to the linker after its own flags, so lower the deployment target
# to 12.0 to match Info.plist's LSMinimumSystemVersion and the fsearch binaries.
export MYGO_LDFLAGS="${MYGO_LDFLAGS:--macos=12.0.0}"

# Rewriting the bundle while it is running makes macOS reject the nested
# fsearch with "Code Signature Invalid", so stop our own running copy first.
for variant in darwin-arm64 darwin-universal; do
  app_bin="$root/build/$variant/Fsearch-Mygo-GUI.app/Contents/MacOS/Fsearch-Mygo-GUI"
  while IFS= read -r pid; do
    [[ -n "$pid" ]] || continue
    echo "==> Quitting running app (pid $pid)"
    kill -TERM "$pid" 2>/dev/null || true
  done < <(pgrep -f "$app_bin" || true)
done
sleep 1

echo "==> Merging and signing resources/fsearch"
bash resources/merge-fsearch.sh

echo "==> Running tests"
go test -race ./...

echo "==> Building arm64 app"
go tool mygo build -platform darwin/arm64

echo "==> Building universal app"
go tool mygo build -platform darwin/universal

echo
echo "==> Done:"
ls -lh build/darwin-arm64/ build/darwin-universal/
