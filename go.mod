module github.com/Japode/cloud/tools/jcloud

// `go` is the language version this code is written against — a minimum, and not a
// pin. `toolchain` is the pin: it names the exact compiler every `go` command in
// this module uses, downloading it where a machine has an older one, so a run on a
// workstation and a run on the server are the same build and not two.
//
// It has to agree with the digest scripts/check-jcloud.sh pins the golang image at,
// and that script asserts the two rather than trusting them — a comment saying they
// agree is a comment that goes stale on the first image bump.
go 1.23

toolchain go1.23.12

require (
	filippo.io/age v1.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/crypto v0.24.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
)
