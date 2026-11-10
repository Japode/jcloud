# jcloud

jcloud checks a Japode Cloud service manifest (`japode.yml`) against the files that have
to agree with it: the service's own Dockerfile, Caddyfile and workflows, and the compose
file, route, `backup.conf` and `.env.example` the platform deploys beside it. It does not
render or deploy anything, and only `env` writes a file.

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
                                                services/*/ declares
jcloud observe [--root <dir>]                   run on the host: print what survey
                                                reads, from the local docker
jcloud up [<service> …] [--root <dir>] …        what stands between a service and a deploy
jcloud env <verb> …                             the age-encrypted .env.age: list, get,
                                                set, unset, render
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
host:                                  # absent: nothing about the host is asserted
  timers: [japode-backup]              # each <unit>.timer must be active
  paths:
    - path: .                          # relative to the root, or absolute
      mode: "750"
      owner: runner:runner             # optional
  firewall: ufw                        # must be installed and active
```

A manifest names its own keys' prefix with `env_prefix: VIGLET_TURING_` (the default is
its name, upper case, `-` as `_`). A variable with `shared: true` is set by its bare
name, such as `OPENAI_API_KEY`, and every manifest declaring it has to agree on its
default and on `secret`.

jcloud refuses a misspelt key or a path outside the root rather than falling back to the
default. It reads this file and never generates one.

`observe` runs on the server, in the deployed tree, and reads the same file. It asks the
local `docker` which containers are up (stripping `container_prefix`), how each one stands
and which digest it runs, reads each service's pin from `pin_file`, and reports each
profile a manifest declares as `on` when `COMPOSE_PROFILES` names it or one of its
services is up. When `jcloud.yml` declares `host:`, it also reads those timers
(`systemctl is-active`), paths (`stat`) and the firewall (`ufw status`), and `survey`
reports each one that differs or could not be read. It reaches no network. Getting its output to `survey` is the platform's
job, for example `ssh host 'cd /srv/platform && jcloud observe' > observed.txt`.

With `kind: nginx`, `agree --routes` takes the output of `nginx -T`. Each manifest route
is the `server` whose `server_name` lists its hosts, and within it `location /`. jcloud
compares `proxy_pass` (a `set $var` or an `upstream` block is resolved),
`client_max_body_size` (nginx's `m` is a MiB) and `proxy_set_header`, where an empty
value is a strip. Open-source nginx has no active health check, so `health_uri` is not
compared. A route's `auth_request` (or Caddy's `forward_auth`) is compared with the
manifest route's `gate:`, the URI of that subrequest, in both directions.

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
