#!/usr/bin/env bash
# The watch, run on the host by jcloud-watch.service (JCT28). A template: copy it into
# the platform, and set what it reads in the service's Environment= lines.
#
#   JCLOUD_ROOT    the deployed tree, where jcloud.yml and the manifests are (required)
#   JCLOUD_STATE   where the last run is kept (default /var/lib/jcloud-watch)
#   JCLOUD         the jcloud binary (default: jcloud on PATH)
#
# Each run observes the host, surveys it against the last run, and leaves three files
# in JCLOUD_STATE: observed.last (what the next run compares with), report.json (every
# finding with its code, subject and state: new, standing or gone) and report.txt (the
# same, for a person). The prose also goes to the journal.
#
# The exit code is survey's: 0 the server is what the tree declares, 1 it is not, so
# the unit shows in `systemctl --failed` and an OnFailure= the platform adds can carry
# it further. jcloud itself alerts nobody and repairs nothing.
set -euo pipefail

ROOT="${JCLOUD_ROOT:?set JCLOUD_ROOT to the deployed tree}"
STATE="${JCLOUD_STATE:-/var/lib/jcloud-watch}"
JCLOUD="${JCLOUD:-jcloud}"

mkdir -p "$STATE"

# A failed observe stops here and the last run is kept: it is still the last thing
# known, and an empty file would read as a server running nothing.
"$JCLOUD" observe --root "$ROOT" >"$STATE/observed.new"

since=()
[ ! -s "$STATE/observed.last" ] || since=(--since "$STATE/observed.last")

verdict=0
"$JCLOUD" survey --root "$ROOT" ${since[@]+"${since[@]}"} "$STATE/observed.new" 2>&1 |
    tee "$STATE/report.txt" || true
"$JCLOUD" survey --root "$ROOT" ${since[@]+"${since[@]}"} --json "$STATE/observed.new" \
    >"$STATE/report.json" || verdict=$?

# 2 is a survey that could not run (a tree it cannot read, a bad argument). The
# observation is not rotated in then, so the next run still compares with the last
# one that was surveyed.
[ "$verdict" -le 1 ] || exit "$verdict"
mv "$STATE/observed.new" "$STATE/observed.last"
exit "$verdict"
