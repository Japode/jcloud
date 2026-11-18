# jcloud

jcloud checks a Japode Cloud service manifest (`japode.yml`) against the files that have
to agree with it: the service's own Dockerfile, Caddyfile and workflows, and the compose
file, route, `backup.conf` and `.env.example` the platform deploys beside it. It does not
render or deploy anything, with one exception: moving a service to another server. Four
commands change something: `env`, the encrypted configuration; `export`, which packs one
service's files and data into an archive; `install`, which unpacks that archive on the new
server and starts the service; and `host apply`, which prepares an empty server from
`jcloud.yml`'s `host:`. Three more run the server's jobs from what the platform declares,
so a timer runs a jcloud command instead of a script the platform has to carry: `backup`
to a restic repository, `restore-test`, which restores the newest snapshot and starts
it, and `healthwatch`, which posts to the platform's webhook when a service goes down
or comes back. And `deploy` brings a service up at the digest its CI published, and
returns to the previous digest when it does not come up healthy.

The product page, with a real run of every verb, is at https://japode.com/jcloud/ (built
from [`site/`](site/README.md)).

```
jcloud check <japode.yml> …                     validate a manifest
jcloud check --repo <root> [--repository o/n]   …and compare it against the service's
      [--dockerfile p] [--caddyfile p] <yml> …  own repository
jcloud agree [--routes <file>] <root> <yml> …    …and compare it against the platform
                                                files deployed beside it; the route
                                                is read from `caddy adapt` JSON or
                                                `nginx -T` output; --compose <file>
                                                takes `docker compose --profile '*'
                                                config --no-interpolate --format
                                                json` in place of each service's
                                                own compose file
jcloud compare <ours.yml> <theirs.yml>          where two copies of a manifest disagree
jcloud survey [--root <dir>] <observations>     the server as observed, against what
      [--since <previous observations>]         services/*/ declares; --since marks
                                                each finding new or standing, and
                                                lists the ones gone; --json prints
                                                each with a stable code (such as
                                                disk.low) and its subject
jcloud observe [--root <dir>]                   run on the host: print what survey
                                                reads, from the local docker
jcloud up [<service> …] [--root <dir>] …        what stands between a service and a deploy
jcloud env <verb> …                             the age-encrypted .env.age: list, get,
                                                set, unset, render
jcloud export <service> [--root <dir>] -o <f>   run on the host: one service's
      --recipient <age key> … [--identity <f>]  compose, route, manifest, addons,
      [--stop]                                  edge files and image pin, in a
                                                .tar.gz with a sha256 per member,
                                                the values of the keys it reads
                                                sealed to each recipient, each
                                                volume as a tar and each database
                                                as a dump, and the edge's
                                                certificates for the route's
                                                hosts, sealed (Caddy only);
                                                --stop stops the
      [--leave <path>] …                        service while volumes are copied;
                                                a mount the manifest does not
                                                declare refuses the export unless
                                                --leave names it
jcloud install <archive> [--root <dir>]         run on the target: lay an export
      [--identity <f>] [--wait-timeout <s>]     down in an empty place, restore
      [--registry-token-file <f>                its data before it starts, start
       --registry-user <name>]                  it and the edge; every pinned
                                                image is pulled first, logged in
                                                with the token when one is given;
                                                the certificates go into an edge
                                                it creates, before it starts, so a
                                                hosts-file test answers over HTTPS
                                                before DNS moves;
                                                host apply runs first when the
                                                archive's jcloud.yml has host:,
                                                and what install writes is
                                                handed to host.user
jcloud host apply [--root <dir>]                as root: make the machine what
                                                host: declares, then survey it
jcloud backup [--root <dir>]                    every named volume to the restic
                                                repository .env names, each sqlite
                                                file with VACUUM INTO and each
                                                mysql volume as a dump
jcloud restore-test [--root <dir>]              restore the newest snapshot into a
      [--wait-timeout <s>] [--cleanup]          project of its own and ask it
                                                whether it works
jcloud healthwatch [--root <dir>]               say when a service goes down or
                                                comes back, to stdout and to
                                                ALERT_WEBHOOK_URL
jcloud deploy [<service> …] [--root <dir>]      pin --image (a digest), check the
      [--image <ref@sha256:…>]                  configuration, pull, bring the
      [--wait-timeout <s>] [--first-boot <s>]   stack up, roll back to the previous
                                                digests when a container is
                                                unwell, reload the edge; set
                                                `deploy: jcloud deploy {name}
                                                --image {image}` for `up` to print it
jcloud version
```

Exit codes: `0` everything checked passed, `1` something was refused, `2` the arguments
were wrong. Problems are reported all at once, and inside GitHub Actions each one is also
printed as an `::error file=…::` annotation. jcloud prints annotations only when
`GITHUB_ACTIONS=true`. A step that runs it in a container passes the variable through
(`docker run -e GITHUB_ACTIONS …`).

## Another platform's layout

`agree`, `survey` and `up` read the platform from its root. By default they expect the
Japode layout. A platform that keeps the same files under other names puts a `jcloud.yml`
at its root saying where they are. Every key is optional and defaults to the value shown:

```yaml
manifest: services/{name}/japode.yml   # compose file and backup.conf sit beside it
service_dir: services/{name}           # or say where they sit
route:
  path: caddy/sites/{name}.caddy       # nginx: one file for every host, no {name}
  kind: caddy                          # or nginx
container_prefix: japode-
image_var: "{NAME}_IMAGE"              # {NAME}: the short name, upper case, - as _
pin_file: .deploy-state/images.env
deploy: DEPLOY_SERVICE={name} DEPLOY_IMAGE={image} bash scripts/deploy.sh {name}
addon: addons/{type}/addon.yml
backup: backup-conf                    # or none: no backup.conf compared, and
                                       # each sqlite file or database named
builds: false                          # true: a manifest may say build: <Dockerfile>
env_example: .env.example              # the template agree holds every key to
carry: [scripts]                       # more of the tree export takes along
host:                                  # absent: nothing about the host is asserted
  timers:                              # each <unit>.timer must be active and
    - japode-healthwatch               # its unit's last run must have succeeded
    - {name: japode-backup, max_age: 7h}  # and finished within max_age
    - name: japode-restore-test        # with run: and on:, host apply writes
      run: scripts/restore-test.sh     #   its units and enables it; the script
      on: "Sun *-*-* 04:40:00"         #   must sit under a carry: path
      stop_post: scripts/restore-test.sh --cleanup  # also: timeout, jitter,
      env: [BACKUP_PASSWORD]           #   accuracy, persistent, success_exit,
                                       #   description; env keys travel sealed
    - name: japode-backup              # or a jcloud verb (backup, restore-test,
      run: jcloud backup               #   healthwatch): run by this binary with
      on: "*-*-* 03,09,15,21:20:00"    #   --root set, no script and no carry:
  paths:
    - path: .                          # relative to the root, or absolute
      mode: "750"
      owner: runner:runner             # optional
  firewall:                            # or just `firewall: ufw`
    kind: ufw                          # must be installed and active
    allow: [OpenSSH, 80/tcp, 443/tcp]  # and, while active, allow each rule
  user: {name: runner, groups: [docker]}  # the deploy account, in each group
  sysctl:                              # each kernel setting must read this value
    net.ipv6.conf.all.disable_ipv6:
      value: "1"
      why: no name here has an AAAA record  # travels into the report
  requires: [docker, compose, rsync]   # each on PATH (compose: the plugin)
  disks: [/, /var/lib/docker]          # each filesystem must keep
  disk_free: 15%                       #   this much free
  certs: [caddy/certificates/*/*.crt]  # each certificate file (a glob) must stay
  cert_days: 14                        #   valid this many days more
```

A manifest names its own keys' prefix with `env_prefix: VIGLET_TURING_` (the default is
its name, upper case, `-` as `_`). A variable with `shared: true` is set by its bare
name, such as `OPENAI_API_KEY`, and every manifest declaring it has to agree on its
default and on `secret`.

A platform of one service, such as a site's own repository with a `jcloud/` folder, can
name its manifest by one literal path with no `{name}`, for example
`manifest: ../japode.yml`. The service's name is then read from the manifest's `name:`.
That literal path is the only one that may leave the root, and when it does,
`service_dir:` has to say where the compose file and `backup.conf` sit inside it.
`export` carries such a manifest as `jcloud-manifest.yml`. `install` into a root that has its
own `jcloud.yml` puts each of the service's files where that layout says. A file the
target already declares is kept, and a difference from the archive is reported, never
refused. The target's `host:` and its edge compose file are the ones used.

jcloud refuses a misspelt key or a path outside the root rather than falling back to the
default. It reads this file and never generates one.

`observe` runs on the server, in the deployed tree, and reads the same file. It asks the
local `docker` which containers are up (stripping `container_prefix`), how each one stands
and which digest it runs, reads each service's pin from `pin_file`, and reports each
profile a manifest declares as `on` when `COMPOSE_PROFILES` names it or one of its
services is up. When `jcloud.yml` declares `host:`, it also reads those timers
(`systemctl is-active` and each unit's last run), paths (`stat`), the firewall and its
rules (`ufw status`), the user (`id -nG`), kernel settings (`sysctl -n`), the required
tools (`command -v`, and `docker compose version`), free space (`df`) and certificate
expiry (read from the file, never by connecting), and `survey` reports each one that
differs or could not be read. It reaches
no network. Getting its output to `survey` is the platform's
job, for example `ssh host 'cd /srv/platform && jcloud observe' > observed.txt`.

With `kind: nginx`, `agree --routes` takes the output of `nginx -T`. Each manifest route
is the `server` whose `server_name` lists its hosts, and within it `location /`. jcloud
compares `proxy_pass` (a `set $var` or an `upstream` block is resolved),
`client_max_body_size` (nginx's `m` is a MiB) and `proxy_set_header`, where an empty
value is a strip. Open-source nginx has no active health check, so `health_uri` is not
compared. A route's `auth_request` (or Caddy's `forward_auth`) is compared with the
manifest route's `gate:`, the URI of that subrequest, in both directions.

## Watching a server

`contrib/` holds two templates a platform copies, which jcloud never runs. Both observe
the host, run `survey --since` against the previous run kept on that machine, and write
`report.json`:

- `contrib/systemd/`: a script, service and timer that run every 15 minutes. The unit
  fails when the server diverges, so it shows in `systemctl --failed`, and the platform
  can add an `OnFailure=`.
- `contrib/github/jcloud-watch.yml`: a workflow for a self-hosted runner on the server.
  It keeps one GitHub issue per finding, keyed by code and subject. A new finding opens
  an issue, a change in wording updates it, and a finding that is gone closes it.

jcloud itself keeps no state, sends no alert and repairs nothing.

## Installing it

Each `v*` tag is a release. Pick one of three ways to run it:

- **A binary**: download the archive for your platform from
  [Releases](https://github.com/Japode/jcloud/releases), check it against `checksums.txt`,
  and put `jcloud` on your `PATH`. Linux, macOS and Windows, amd64 and arm64.
- **With Go**: `go install github.com/Japode/jcloud@latest`, or a fixed version with
  `go run github.com/Japode/jcloud@v0.3.0 check japode.yml`.
- **What the platform does**: it pins the tag and builds it inside a Go image pinned by
  digest, so nothing is installed on the host.

`jcloud version` prints the release, the schema version and the compiler that built it.

## Releasing

Push a `v*` tag. `.github/workflows/release.yml` runs `ci/check.sh`, then GoReleaser
(`.goreleaser.yaml`) builds with the compiler `go.mod` pins and publishes the archives and
`checksums.txt` to a GitHub Release. The version comes from the tag; there is no constant to
bump. `.goreleaser.yaml` has the command for a local snapshot build that publishes nothing.

## Developing

```sh
bash ci/check.sh                          # toolchain pin, gofmt, go vet, go test
JCLOUD_PLATFORM=../cloud bash ci/check.sh # plus the contract tests against the platform
```

Docker is the only requirement. The Go toolchain comes from the image digest in
`ci/check.sh`, and `go.mod`'s `toolchain` line must name the same compiler; the check
fails when they differ.

Some tests are contracts with the platform repository: its committed manifests and addons,
its top-level compose file, and two of its scripts. They read the checkout named by
`JCLOUD_PLATFORM` and skip without it.

## License

[Apache-2.0](LICENSE).
