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

### §JCT32 jcloud export: the archive and its file set

`jcloud export <service> [--root <dir>] -o <file>.tar.gz` runs on the source host, in
the deployed tree, and writes one archive. This task is the skeleton the next ones fill:
the format and the platform files, no secrets and no data yet.

The file set comes from what `agree` already resolves through jcloud.yml: the manifest,
its compose file and backup.conf, the route (`route.path` for that name; for nginx, the
one shared file, flagged as such), each addon a `needs:` names, the top-level compose
that runs the edge and its Caddyfile, jcloud.yml itself, and the service's lines from
`pin_file`, so the target pulls the same digests the source runs. Nothing outside the
root is read, as everywhere else.

At the archive's root sits `jcloud-export.json`: schema version, jcloud version, service
name, source host, time, and a sha256 per member. `install` refuses an archive whose
checksums or schema it does not know.

For fespsp-institucional that is services/fespsp-institucional/*,
caddy/sites/fespsp-institucional.caddy, addons/mariadb/addon.yml, docker-compose.yml,
caddy/Caddyfile and the FESPSP_INSTITUCIONAL_IMAGE pin.

This is the first verb besides `env` that writes a file, and the README's opening
sentence changes with it.

### §JCT33 export carries the env, encrypted

`export` reads the value of every key the manifest declares (with its `env_prefix`, plus
each `shared: true` key by its bare name) from the deployed `.env`, or from `.env.age`
through the same code `jcloud env render` uses, and writes them as `env.age` inside the
archive, encrypted to `--recipient <age public key>` (repeatable). No recipient, no
export: an archive with the database password and the SMTP credential in clear is a file
that leaks by being copied.

Only that service's keys travel. A server holding several services has a `.env` with all
of them, and the target receives this one's.

A key the manifest declares with no value on the source and no default is reported, not
invented. `install` will refuse to start the service with it missing, which is the same
rule `agree` holds the `.env.example` to.

For fespsp-institucional that is the 14 FESPSP_INSTITUCIONAL_* keys the manifest does
not fix with `value:`, three of them secret: WORDPRESS_DB_PASSWORD, WP_ADMIN_PASS and
SMTP_PASS. Keys the edge needs (FESPSP_INSTITUCIONAL_HOSTS, read by the Caddy route) are
found by reading the route for `{$…}` placeholders, not by a list kept here.

### §JCT34 export carries the data

Each volume the manifest declares becomes `data/<volume>.tar` in the archive, read
through a throwaway container mounting the volume read-only (the restic drill's way in,
without restic). Each `needs:` whose addon declares a dump becomes `data/<need>.sql`,
made the way the addon's backup directive says: for mariadb, `mariadb-dump
--single-transaction` as MARIADB_USER over the one database, which is consistent while
the site keeps serving. SQLite files named in backup.conf are copied with `.backup`,
never as bytes mid-write.

Volumes are not consistent the same way: a file uploaded during the copy may be missing.
`--stop` stops the service (not its database) for the duration and starts it again,
which is what the final export before a cutover wants; without it the export says the
volumes were copied live.

The archive records each volume's size and each dump's table count, so `install` can say
the restore came back whole rather than merely ran.

For fespsp-institucional: `fespsp-institucional-uploads` as a tar and `db` as
`fespsp.sql`. WordPress core and the themes are not data: they come from the image.

### §JCT35 export names the state it leaves behind

fespsp-institucional's compose keeps DISALLOW_FILE_MODS off so wp-admin can install
plugins, and its own comment says where they land: wp-content/plugins, in the
container's anonymous volume, not backed up and not declared. A move built from the
manifest alone would carry the uploads and the database and arrive with every plugin
installed through wp-admin gone, while the healthcheck (the REST index) still passes.

`export` therefore inspects each of the service's running containers (`docker inspect`
mounts) and lists every volume or bind mount that the manifest does not declare. Each
one is a refusal unless `--leave <path>` says it is known and meant to stay behind, or
the manifest is changed to declare it. The remedy names both doors.

The fix for this service belongs in the platform, not here: a named volume for
wp-content/plugins in Japode/cloud's compose and manifest (a task for that repository).
Once it is declared, JCT34 carries it like the uploads.

The same check would have caught any service whose state moved to a path nobody
declared, which is the drift `agree` cannot see because it reads files, not the running
container.

### §JCT36 jcloud install: lay down, restore, start, prove

`jcloud install <archive> --root <dir> --identity <age key>` runs on the target and
does, in this order, what docs/DEPLOY.md does by hand:

1. Verify `jcloud-export.json`: schema, checksums, and that docker and compose answer. Refuse a root where the service already has containers or volumes: install is for an empty place, never an overwrite, so it cannot be mistaken for a deploy.
2. Lay down the files under `--root`; merge the service's keys into `.env` (mode 600) and its pins into `pin_file`, keeping lines already there.
3. Create the volumes, untar each one, start only the database needs, wait for their healthchecks, feed each dump in.
4. Start the service and the edge, and wait for the healthcheck under the same budget a deploy uses.
5. Report what came back against what the archive recorded: volume sizes, table counts, health. Then `agree` on the laid-down tree.

Step 3 before step 4 is load-bearing. fespsp's entrypoint runs the seed only when `wp
core is-installed` fails, and the seed rewrites pages. Started on an empty database, it
installs a demo site and the dump arrives on top of it, with seed media left in uploads.

Not done: DNS. The edge gets its certificate on the first request after the name points
here; install prints that as the remaining step, with the host names the route answers
for.

### §JCT37 export --images: the images inside the archive

By default the archive carries pins, not images: the target pulls the same digests. That
fails where the image is private and the new host has no `docker login` for ghcr.io, and
on this platform's provider, where large pulls over IPv6 were measured to reset (cloud
JC102).

`export --images` adds `images/<name>.tar` from `docker save` of each pinned digest, the
service's and each addon's. `install` loads them before step 3 and checks the loaded
digest equals the pin, so an archive cannot swap an image under the same name.

The cost is size: fespsp-institucional's image is about 280 MB, which is why it is a
flag and not the default. Open question for the owner: is the GHCR package public, and
will the new server get a registry token? If both are yes, this stays an idea.

### §JCT38 A host install cannot start on

"An empty server" in the request means a fresh machine. JCT36 assumes docker and compose
answer, and refuses otherwise. Two ways forward, not decided:

- **Refuse with the remedy** (smallest): install checks docker, compose, free ports 80/443 and, when jcloud.yml declares `host:`, prints what `survey` would hold the machine to. The platform's own bootstrap stays the platform's. For Japode that is `sudo bash scripts/bootstrap-server.sh`, which the archive already carries if JCT32 includes `scripts/`.
- **`install --bootstrap`**: jcloud installs docker and sets up the user, directory modes and ufw itself. This makes jcloud a provisioning tool, a different product from one that checks and moves services, and it would duplicate a script the platform already tests.

The recommendation is the first, with the archive carrying the platform's bootstrap
script so the empty server needs only the archive and jcloud. Decide before JCT36 is
built, because it fixes what step 1 checks.

### §JCT39 install --host: a move to a new name

The safe way to move fes-inst.sumware.com.br is to install it first under a test name,
check it, then point DNS. Changing `FESPSP_INSTITUCIONAL_HOSTS` and `WP_URL` in the
target's `.env` covers the route and WP_HOME, but WordPress writes absolute URLs into
post content and options, so links and images still point at the old host.

`install --host <name>` would set every key whose default is the route's host, and then
run a rewrite the manifest declares, because only the service knows how its data names
itself. For WordPress that is `wp search-replace <old> <new> --all-tables` inside the
container. A manifest field such as `rehost: [cmd…]` with `{from}` and `{to}` keeps
jcloud free of WordPress knowledge.

An idea until the first move: if the plan is to cut DNS straight to the new server under
the same name, nothing here is needed.
