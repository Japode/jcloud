# jcloud

jcloud checks a Japode Cloud service manifest (`japode.yml`) against the files that have
to agree with it: the service's own Dockerfile, Caddyfile and workflows, and the compose
file, route, `backup.conf` and `.env.example` the platform deploys beside it. It does not
render or deploy anything, and only `env` writes a file.

```
jcloud check <japode.yml> …                     validate a manifest
jcloud check --repo <root> [--repository o/n]   …and compare it against the service's
      [--dockerfile p] [--caddyfile p] <yml> …  own repository
jcloud agree [--adapted caddy.json] <root> <yml> …
                                                …and compare it against the platform
                                                files deployed beside it
jcloud compare <ours.yml> <theirs.yml>          where two copies of a manifest disagree
jcloud survey [--root <dir>] <observations>     the server as observed, against what
                                                services/*/ declares
jcloud up [<service> …] [--root <dir>] …        what stands between a service and a deploy
jcloud env <verb> …                             the age-encrypted .env.age: list, get,
                                                set, unset, render
jcloud version
```

Exit codes: `0` everything checked passed, `1` something was refused, `2` the arguments
were wrong. Problems are reported all at once, and inside GitHub Actions each one is also
printed as an `::error file=…::` annotation.

## Running it

The platform runs it by version, in a Go image pinned by digest:

```sh
go run github.com/Japode/jcloud@v0.1.0 check japode.yml
```

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
