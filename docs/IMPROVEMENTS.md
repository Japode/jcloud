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

## Block F — Moving a service: one archive carries it to an empty server

### §JCT42 export carries the route's certificate

The owner tests a move by pointing a workstation's hosts file at the new server under
the real name (JCT39 was retired for that). Over HTTPS this fails: the new edge asks
ACME for a certificate, the HTTP-01 challenge reaches the old server, and the browser
gets no certificate at all. The site is fine and nobody can see it.

`export` reads, from the edge's data volume, the certificate, key and metadata Caddy
stores for each host the route answers for (`caddy/certificates/<issuer>/<host>/`), and
nothing else of that volume. The key is sealed to the same age recipients as the env
(JCT33) and never written in clear. `install` puts them at the same path in the new
edge's volume before it starts the edge, so Caddy serves the existing certificate at
once and renews it through ACME once DNS has moved.

Not carried: the ACME account key and the other hosts' certificates. The new server
registers its own account on its first renewal, which costs nothing for one service.

A certificate with fewer than `cert_days` left (or 14) is reported at export, since it
may expire before the cutover. With `kind: nginx` the certificate's paths come from the
route's `ssl_certificate` lines; anything else is refused by name.

Done when a hosts-file test of an installed fespsp-institucional answers over HTTPS with
the source's certificate.

### §JCT43 host: timers that host apply installs

Japode/cloud's jcloud.yml declares three timers (healthwatch, backup, restore-test), and
`host apply` leaves them out on purpose: today they are written by
scripts/install-timers.sh, inline heredocs around scripts that run from the deploy
directory (backup.sh with backup-lib.sh, restore-test.sh, healthwatch.sh). The archive
carries none of `scripts/`, so a server built by `install` serves the site and has no
backup, no drill and no watch, and `survey` is the first to say so.

Two changes:

- A timer entry states what it runs and when: `{name: japode-backup, run: scripts/backup.sh, on: "*-*-* 03,09,15,21:20", max_age: 7h}`. `host apply` writes the service and timer units from that (After=docker.service, the deploy user, the root as working directory) and enables them. An entry without `run:` stays survey's alone, as today.
- jcloud.yml gains `carry: [scripts/]`, extra tree paths `export` includes with their checksums. Each `run:` must sit under a carried path, or the export is refused, since a script `source`s neighbours jcloud cannot follow.

Then `install` ends with the timers running, and Japode/cloud can drop install-timers.sh
the way JC108 dropped the bootstrap.

Done when an installed fespsp-institucional has all three timers active and `survey`
reports no timer difference.
