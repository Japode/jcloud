#!/usr/bin/env bash
set -euo pipefail

# Build jcloud and run its tests in the pinned Go toolchain.
#
#   bash ci/check.sh
#
# Run in CI (.github/workflows/ci.yml) and by hand before pushing. The toolchain
# arrives as an image pinned by digest and nothing is installed on the host, so this
# works on a machine with no Go at all and every run uses the same compiler.
#
# What it asserts: go.mod's `toolchain` line names the compiler the image runs,
# nothing is unformatted, and `go vet` and the unit tests pass. One thing it reports
# without failing: a dependency whose newest release needs a newer Go than this pin.
#
# The committed-tree tests (TestCommitted*) read a platform repository and skip
# without one. Set JCLOUD_PLATFORM to a checkout of it to run them too:
#
#   JCLOUD_PLATFORM=../cloud bash ci/check.sh
#
# The platform's own scripts/check-jcloud.sh also runs `jcloud agree` over every
# manifest it holds, which covers the same ground.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# go 1.26.8, by digest. go.mod's `toolchain` line has to name the same compiler, and
# the first stage below fails when they differ.
GO_IMAGE="${GO_IMAGE:-golang@sha256:6c2a5538f964f1c82f97ad14988bf05de100d922d159d0e398b54c7b0ca0c6c9}"

die() { printf '\n\033[31mfailed: %s\033[0m\n' "$*" >&2; exit 1; }
command -v docker >/dev/null || die "no docker: this needs it to reach the pinned Go toolchain"

# Git Bash rewrites a leading / in arguments to a Windows path, which turns the
# container's /src into nonsense. `pwd -W` gives docker a path it can mount.
if [ -n "${MSYSTEM:-}" ]; then
    export MSYS_NO_PATHCONV=1
    ROOT="$(cd "$ROOT" && pwd -W)"
fi

PLATFORM_MOUNT=()
if [ -n "${JCLOUD_PLATFORM:-}" ]; then
    platform="$(cd "$JCLOUD_PLATFORM" && { [ -n "${MSYSTEM:-}" ] && pwd -W || pwd; })"
    PLATFORM_MOUNT=(-v "$platform:/platform:ro" -e JCLOUD_PLATFORM=/platform)
fi

# shellcheck disable=SC2016  # every $ below is the inner shell's
docker run --rm \
    -v "$ROOT:/src" \
    -v jcloud-gocache:/go/pkg/mod \
    -e GOCACHE=/tmp/gocache \
    ${PLATFORM_MOUNT[@]+"${PLATFORM_MOUNT[@]}"} \
    -w /src \
    "$GO_IMAGE" sh -c '
    set -eu

    printf "\n    toolchain\n"
    pinned="$(sed -n "s/^toolchain //p" go.mod)"
    running="$(go env GOVERSION)"
    if [ "$pinned" != "$running" ]; then
        printf "go.mod pins %s and the image runs %s: bump whichever is behind\n" \
            "$pinned" "$running" >&2
        exit 1
    fi
    printf "      %s, pinned by go.mod and by the image digest\n" "$running"

    printf "    gofmt\n"
    # Listed and not applied: a check that rewrites the tree passes by editing what
    # it was asked about.
    unformatted="$(gofmt -l .)"
    if [ -n "$unformatted" ]; then
        printf "gofmt would change: %s (run gofmt -w on them)\n" "$unformatted" >&2
        exit 1
    fi

    printf "    go vet\n"
    go vet ./...
    printf "    go test\n"
    go test ./...

    # A warning and never a failure: the condition is somebody else publishing a
    # release, and no commit here caused it. GOPROXY=off and an unreachable proxy
    # both say so rather than reporting that nothing is behind.
    printf "    the ceiling this toolchain puts on its dependencies\n"
    pinned="${running#go}"
    if [ "$(go env GOPROXY)" = "off" ]; then
        printf "::warning::the dependency ceiling went uncompared: GOPROXY is off\n"
    elif updates="$(go list -m -u -f "{{.Path}} {{if .Update}}{{.Update.Version}}{{end}}" all 2>/dev/null)"; then
        ceiling="$(printf "%s\n" "$updates" |
            while read -r path newest; do
                [ -n "$newest" ] || continue
                needs="$(go list -m -json "$path@$newest" 2>/dev/null |
                    sed -n "s/^[[:space:]]*\"GoVersion\": \"\([^\"]*\)\".*/\1/p")"
                [ -n "$needs" ] || continue
                top="$(printf "%s\n%s\n" "$needs" "$pinned" | sort -V | tail -n1)"
                [ "$top" = "$needs" ] && [ "$needs" != "$pinned" ] || continue
                printf "::warning::%s %s requires Go %s and go.mod pins %s, so go get will take an older one\n" \
                    "$path" "$newest" "$needs" "$pinned"
            done)"
        if [ -n "$ceiling" ]; then
            printf "%s\n" "$ceiling"
        else
            printf "      nothing published needs a newer Go than %s\n" "$pinned"
        fi
    else
        printf "::warning::the dependency ceiling went uncompared: the module proxy did not answer\n"
    fi
' || die "jcloud does not build, is unformatted, or its tests fail"

printf '\n\033[32mjcloud builds and its tests pass\033[0m\n'
