#!/usr/bin/env bash
set -euo pipefail

# Capture every terminal on the page from a released jcloud (JCT12).
#
#   bash site/scripts/capture.sh v0.2.0
#
# Nothing the page shows as jcloud output is typed by hand: copy that drifts from the
# binary is the drift jcloud exists to stop. This builds the tag the way GoReleaser does
# (.goreleaser.yaml: CGO off, -trimpath, main.version set from the tag) in the Go image
# ci/check.sh pins, for Linux, so the paths read with / on every machine. Then it adapts
# the demo route with the caddy image the platform runs, and runs site/scripts/capture.mjs
# in a Node image, which runs each scenario and writes site/src/transcripts.json.
#
# Docker is the only requirement. Run it again after a release, or after changing a
# scenario under site/demo/, and commit what it writes.

TAG="${1:?usage: capture.sh <tag>, e.g. v0.2.0}"
SITE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$SITE/.." && pwd)"

GO_IMAGE="$(sed -n 's/^GO_IMAGE="\${GO_IMAGE:-\(.*\)}"$/\1/p' "$ROOT/ci/check.sh")"
[ -n "$GO_IMAGE" ] || { echo "capture: could not read GO_IMAGE from ci/check.sh" >&2; exit 1; }
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2-alpine}"
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine}"

git -C "$ROOT" rev-parse --verify --quiet "refs/tags/$TAG" >/dev/null ||
    { echo "capture: no tag $TAG in this repository" >&2; exit 1; }

OUT="$SITE/.capture"
rm -rf "$OUT" && mkdir -p "$OUT/src"
git -C "$ROOT" archive "$TAG" | tar -x -C "$OUT/src"

# Git Bash rewrites a leading / in arguments to a Windows path; `pwd -W` gives docker a
# path it can mount.
mount() { if [ -n "${MSYSTEM:-}" ]; then (cd "$1" && pwd -W); else echo "$1"; fi; }
[ -n "${MSYSTEM:-}" ] && export MSYS_NO_PATHCONV=1

printf '==> building jcloud %s for linux in %s\n' "$TAG" "${GO_IMAGE%%@*}"
docker run --rm \
    -v "$(mount "$OUT"):/out" \
    -v jcloud-gocache:/go/pkg/mod \
    -e GOCACHE=/tmp/gocache -e CGO_ENABLED=0 -e GOOS=linux \
    -w /out/src "$GO_IMAGE" \
    go build -trimpath -ldflags "-s -w -X main.version=$TAG" -o /out/jcloud .

printf '==> adapting the demo route with %s\n' "$CADDY_IMAGE"
docker run --rm -v "$(mount "$SITE/demo/platform/caddy"):/c:ro" "$CADDY_IMAGE" \
    caddy adapt --config /c/sites/notes.caddy --adapter caddyfile \
    >"$SITE/demo/platform/caddy-adapt.json" 2>/dev/null

printf '==> running the scenarios\n'
docker run --rm -v "$(mount "$SITE"):/site" -w /site -e JCLOUD=/site/.capture/jcloud \
    "$NODE_IMAGE" node scripts/capture.mjs

rm -rf "$OUT"
