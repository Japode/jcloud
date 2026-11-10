# Improvements

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

### §JCT10 A second platform in the contract tests

The harness landed: platform_test.go reads JCLOUD_PLATFORM as a list, runs every
TestCommitted test once per tree through that tree's jcloud.yml, keeps the tests about
Japode's own scripts to a tree with no jcloud.yml, and ci/check.sh mounts each tree in
`JCLOUD_PLATFORM=../cloud:../../viglet/cloud` and skips an absent one.

What is left is jcloud's own CI. .github/workflows/ci.yml checks out no platform, so
there every contract test skips. Japode/cloud and openviglet/cloud are both private, so
checking them out needs a read-only token as a repository secret (a fine-grained PAT
with contents:read on the two, or a GitHub App installed on both orgs). With it, ci.yml
gains two `actions/checkout` steps into sibling paths and passes their list to
ci/check.sh. Which credential, and who owns it, is the user's decision.

Before Viglet Cloud's tree is useful there it needs its own adoption, filed in its own
roadmap: a jcloud.yml, manifests for turing, shio and dumont, its check script writing
.deploy-state/nginx-T.txt and .deploy-state/compose-config.json (platform_test.go names
them), and JCT18 and JCT20 shipped here. Until it carries a jcloud.yml it is read as the
Japode layout and fails the Japode-script tests, which is the right answer for a tree
that has not said what it is.

### §JCT19 One compose path: the model

Since JCT4, `agree --compose` reads the model `docker compose --profile '*' config
--no-interpolate --format json` prints, and without it falls back to agreeCompose and
agreeNeeds over services/<name>/docker-compose.yml as text (compose_model.go). The
Japode platform's scripts/check-jcloud.sh passes only `--adapted`, so its services still
go through the substring path, which passes on a commented-out line.

Measured on 2026-11-10: the model built from the top-level docker-compose.yml plus every
services/*/docker-compose.yml, joined with `-f` the way scripts/deploy.sh joins them,
agrees for all three Japode services and both their databases. So the change is in the
platform's script: build that model into .deploy-state/compose-config.json (the path
platform_test.go reads) and pass it with `--compose`. jcloud then reports an absent
model the way it reports absent routes ("not supplied, so unchecked"), and the textual
compose and needs checks are deleted, leaving one code path.

The same script edit passes `-e GITHUB_ACTIONS` to go_run's `docker run`. Since JCT17
jcloud prints annotations only when that variable is "true", and the container starts
without it, so the platform's CI loses them at the first pin bump past JCT17 unless it
is passed through.

The two repositories land in order: the platform script first, then the jcloud release
that drops the fallback, or the platform's CI turns red on a model it never built.

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

## Block E — Watching the server: jcloud reads the running machine on its own

### §JCT23 The host, declared in jcloud.yml

`jcloud.yml`'s `host:` key shipped (host.go): timers that must be active, path modes and
owners, a firewall, read by `observe` and compared by `survey`. What is left is the
sentence that a manifest whose backup is declared implies its own timer, so the common
case is written once.

It was not built because nothing in a manifest says a service runs a backup unit of its
own. Viglet Cloud names `viglet-fesprojetos-backup` and `viglet-trabalharei-backup`, but
Turing's data is copied by the stack-wide `viglet-postgres-backup`, and Japode has only
stack-wide timers. Deriving one timer per manifest with a volume would report timers
that were never meant to exist.

Build it as `host.backup_timer: "<prefix>{name}-backup"`, applied only to the manifests
that opt in. Which manifest field opts in is the choice still open: a new `backup: own`,
or reading it off what Viglet's manifests say once C190 has written them. Decide against
those manifests, not before.

Once this lands, Japode's `survey-server.sh` can shrink to `ssh … jcloud observe`, with
its timers, mode and firewall moved into its `jcloud.yml`. That switch is filed in the
cloud repo, not here.

### §JCT26 What changed since the last run

Run twice, `survey` prints the same report whether the drift is a year old or an hour
old. A watch run every few minutes then has two bad choices: alert on every run, or
alert on nothing, because it cannot tell a new failure from one everyone already knows.

`survey --since <previous observations>` compares the two runs as well as the tree, and
splits the findings in three: new since the last run, still standing, and gone. The exit
code stays the tree's verdict; the split is what a scheduler acts on.

The previous file is passed, never stored: jcloud keeps no state, so where the last run
lives is the caller's decision, like the transport.

### §JCT27 Findings a scheduler can key on

`survey` prints sentences for a reader. A scheduler that wants one alert per fact has to
parse them, and the day a sentence is reworded the alert closes and a new one opens for
a fact that never changed.

`survey --json` prints each finding with a stable code (`container.missing`,
`digest.moved`, `timer.failed`, `backup.stale`, `disk.low`, `cert.expiring`), the
subject it is about, and with `--since` its state: new, standing or gone. The code and
subject together are the alert's key, so rewording the prose touches nothing.

The prose form stays the default and stays what a person reads in CI.

### §JCT25 Disk and certificates, read on the host

Two failures are seen today only through what they break: a full disk stops postgres and
the builds, and an expired certificate takes every host on it down at once. Both are
facts the host can answer locally, so they fit `observe` without a network reach.

`observe` prints the free space of each filesystem `host:` names and the expiry of each
certificate file the edge serves — the paths `jcloud.yml` gives, read with the local
openssl, never by dialling the host name. `host:` declares the floors: `disk_free: 15%`,
`cert_days: 14`, and `survey` reports one under its floor with how far under it is.

Out of scope: CPU, memory and request latency. They change by the second and want a
time-series store, which is a monitoring system and not a consistency check.

### §JCT28 A watch that runs itself

Every piece above is a command a person has to remember to run. Monitoring is the same
commands run without anyone remembering, and a verdict that reaches someone.

jcloud ships two templates under `contrib/`, read and copied by the platform, never run
by jcloud: a systemd service and timer that run `observe`, then `survey --since --json`
against the last run, and a workflow for a self-hosted runner that does the same and
opens one GitHub issue per new finding, comments on a standing one only when it changes,
and closes it when the finding is gone.

jcloud still writes nothing and alerts nobody: the template does, with the platform's
token, on the platform's machine — JC1's rule that a verdict is read where it is made.
Refused: an alert channel inside jcloud, and any repair the timer runs on its own.

### §JCT29 Viglet Cloud, watched without a collector of its own

The proof that the watch is transparent is a platform adopting it without writing a
collector. Viglet Cloud is that platform: nginx rather than Caddy, client silos
instanced per key, and its own timers for backups, the crawl and `configure-realm`.

Adoption is the files C190 already names (`jcloud.yml`, manifests for Turing, Shio,
Dumont and the silos, a root `.env.example`), plus a `host:` key for its timers, disk
and certificates, plus the JCT28 timer installed on the server. Nothing Viglet-specific
is written in bash.

Anything that adoption has to work around is filed here as its own line, the way JCT21
was.
