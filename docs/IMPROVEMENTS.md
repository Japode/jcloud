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

### §JCT37 install pulls a private image with a registry token

The archive carries pins, not images: the target pulls the same digests the source runs.
fespsp-institucional's package on GHCR is private, and the owner wants it pulled the way
the platform already does it: Japode/cloud's Deploy workflow logs in with
GHCR_READ_TOKEN, a PAT with `read:packages`, because a repository's own GITHUB_TOKEN
reaches only that repository's packages. A fresh server has neither the runner nor that
login.

`install --registry-token-file <path>` (or `JCLOUD_REGISTRY_TOKEN`) logs in to each
registry the pins name, pulls every pinned digest, the service's and each addon's, and
only then touches a volume. The pull moves to the front of JCT36's order: a token that
is wrong or expired then fails the move on an untouched server, not halfway through a
restore.

The token is never written into the archive or the laid-down tree, and the login lands
in the invoking user's docker config, as a manual `docker login` would. `install` says
so, and says how to log out.

Not done here: shipping images inside the archive (`docker save`). It was the
alternative for a host with no registry access at all, and nothing in this move needs
it. The IPv6 pull resets measured on this provider (cloud JC102) are answered by the
host setup in JCT38, not by avoiding the registry.

### §JCT38 jcloud host apply: build the server from host:

Decided by the owner: jcloud installs everything on an empty server, and Japode/cloud's
bootstrap-server.sh is replaced by it rather than kept beside it (cloud task, filed
there). The duplication is avoided by abstraction: the script's steps become `host:`
declarations (JCT40), and `jcloud host apply` makes the machine match them.

`sudo jcloud host apply [--root <dir>]`, idempotent, each step a no-op when it already
holds:

1. Docker engine and the compose plugin, from Docker's own apt repository (Debian and Ubuntu first; another distribution is refused by name, not guessed at), plus each tool in `requires:`.
2. The user and its groups.
3. Each path with its mode and owner.
4. The sysctl entries, in one file under /etc/sysctl.d naming jcloud and each entry's `why:`.
5. The firewall: SSH is allowed before ufw is enabled, never the other way round. Enabling ufw over SSH without that rule locks the machine away from the person running it, which is why the script today only advises.

Then it runs the checks `survey` would and prints any that still differ.

`install` (JCT36) runs `host apply` first when jcloud.yml declares `host:`, so an empty
server needs `jcloud install <archive>` and nothing else.

Outside: the GitHub runner registration, the platform's way of deploying and not part of
a move. A writing verb changes what jcloud is; this one is invoked by a person and never
by the watch, so the non-goal on unattended repair still holds.

### §JCT40 host: declares what a server is built with

Japode/cloud's scripts/bootstrap-server.sh sets up four things `host:` cannot say today:
a non-root deploy user in the docker group, the deploy directory and its pin file (750
and 600, owned by that user), the firewall's open ports (80/tcp, 443/tcp, 443/udp) and a
sysctl file turning IPv6 off on a provider whose v6 path resets large pulls (cloud
JC102). `host.paths` covers the second already; the rest is new:

```yaml
host:
  user: {name: runner, groups: [docker]}
  firewall: {kind: ufw, allow: [OpenSSH, 80/tcp, 443/tcp, 443/udp]}
  sysctl: {net.ipv6.conf.all.disable_ipv6: "1", net.ipv6.conf.default.disable_ipv6: "1"}
  requires: [docker, compose, rsync, git]
```

`observe` reads each one (`id`, `ufw status`, `sysctl -n`, `command -v`) and `survey`
reports each difference with a stable code, like the timers and modes before them
(JCT23). This task only declares and checks. Changing the machine is JCT38.

The sysctl entry keeps the reason the script states: it is right only while no name on
the host has an AAAA record. A `why:` beside an entry carries that sentence into the
file jcloud writes.
