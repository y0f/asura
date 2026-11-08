# Redistribution notices

`go.txt` contains reviewed upstream license texts and additional notices from
the selected Go runtime and module source. `go-modules.json` records the exact
runtime module union for Linux amd64/arm64 and Windows amd64, for both server
builds and the probe agent with CGO disabled. Both supported compilers, Go
1.26.10 and 1.27.3, were audited. This is a source inventory, not a
claim that every listed package survives linker dead-code elimination in every
binary. Test/tool dependencies and the container's operating-system packages
are outside this inventory.

The audit includes module license files, nested runtime notices, generated
SQLite/libc header attributions, Go standard-library and vendored notices,
Unicode data, and referenced license/exception texts. Upstream module archives
are pinned by `go.sum`; use `go mod download -json module@version` to obtain
their exact source. Each section records its source. The Go release sources
are at the versioned `golang/go` links in the notices.

After changing a module version, platform, build tag or Go toolchain, inspect
the actual runtime source and update the inventory and applicable notice
sections. Do not approve a new dependency merely by editing the manifest.
Frontend asset versions and license sources live in `web/vendor.json` and
`web/static/third-party-notices.txt`.

From the repository root:

```sh
go run ./cmd/notices
go run ./cmd/notices -check -verify
```

The first command assembles the canonical `THIRD_PARTY_NOTICES.txt`; the second
checks exact output, the actual compiler version and the reviewed module versions
against all recorded runtime build targets. Release, Docker and source-installer
builds use the same environment: local toolchain, no workspace, no ambient
`GOFLAGS` or `GOEXPERIMENT`, CGO disabled, and baseline `GOAMD64=v1` /
`GOARM64=v8.0`. The server's audited build tags are empty or `postgres`; the
release assets and Docker image use the empty tag. Ordinary developer targets
remain configurable. Other compilers, platforms, architecture levels or build
flags require a separate source/graph audit before redistributing their output.

The command does not download new license texts or infer license compatibility.
Release assets include the combined file and Asura's
own `LICENSE` next to the existing binaries. Container and source-installer
copies are under `/usr/share/licenses/asura/`; the browser assets also carry
their embedded frontend notice file.
