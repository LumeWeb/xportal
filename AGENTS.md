# AGENTS.md

Guidance for AI agents and contributors working in this repository.

## What this repo is

**xportal** is a Caddy-style **portal builder** for LumeWeb's **Portal** (a fork of
Caddy). It compiles a custom Portal binary that embeds a chosen set of plugin modules,
and acts as a drop-in replacement for `go run` while developing Portal plugins.

The tool started as a fork of [`caddyserver/xcaddy`](https://github.com/caddyserver/xcaddy)
(the equivalent tool for Caddy itself) and has diverged to target Portal instead of Caddy.
Upstream credit and license are preserved.

Install: `go install go.lumeweb.com/xportal/xcmd/xportal@latest`

Basic usage:

```bash
xportal build --with go.lumeweb.com/portal-plugin-dashboard@latest
```

- `build [version]` — compile a custom Portal binary (defaults to `PORTAL_VERSION` or latest).
- `scratch <path> [version]` — generate a full build environment at `path`, then stop (dev/setup aid).
- `version` — print the xportal version.
- anything else — build the current module as a plugin and `go run` the result (dev loop).

## Repo layout

| Path | Purpose |
|------|---------|
| `builder.go` | `Builder` type and `Build()` orchestration: `go mod tidy`, `go generate`, `-ldflags` metadata injection, and the final `go build`. |
| `environment.go` | `newEnvironment` (temp module init, `go mod edit -replace`, `go get` pinning), the go-command constructors, and the generated `main.go` template. |
| `io.go` | Recursive `copy` helper (file/dir/symlink), originally from goreleaser. |
| `platforms.go` | `Compile`/`Platform` types and `SupportedPlatforms()` (`go tool dist list`). |
| `cmd/` | Cobra CLI (`cobra.go`, `commands.go`), `default_replacements.go`, entry wiring (`main.go`). |
| `cmd/main.go` | CLI host: env-var handling, output naming, dev-mode `go run` & setcap logic, module-version parsing. |
| `xcmd/xportal/main.go` | Actual `main` package entry point. |
| `internal/utils/` | `GetGo()`/`GetGOOS()`/`GetGOARCH()` and Windows resource embedding (`goversioninfo`). |

## Building & testing

- Build the `xportal` binary: `make` (wraps `go build -ldflags='-s -w ...' -o xportal ./xcmd/xportal`) or `go build ./xcmd/xportal`.
- A `Dockerfile` produces a container image that can build plugin binaries.
- CI lives in `.github/` (build, docker, and release workflows, using Knope for releases).
- The repo has no Go test files; `go test ./...` is a no-op. Verify changes with `go build ./...`, `go vet ./...`, and a manual build.

## How a build works

1. `Builder.newEnvironment` creates a temp folder and initializes a fresh `go mod` there.
2. `go mod edit -replace` applies all replacements (defaults + user `--replace`/`--with`).
3. `go get` pins the Portal core version, then each plugin (passing the core version to
   prevent the plugin from upgrading it), then one final empty `go get`.
4. `go mod tidy -e` then `go generate ./...` run in the build env.
5. Version/commit/branch build metadata for the core **and every plugin** is injected via
   `-ldflags -X` (see divergence §3), plus the protobuf conflict-policy override
   (see divergence §9).
6. `go build -o <output>` with `-trimpath`, stripped symbols, and `-tags nobadger`
   (when no custom `XPORTAL_GO_BUILD_FLAGS` are set).
7. For Windows targets, `goversioninfo` embeds an icon + version resource first.

## Configuration: environment variables

All are `XPORTAL_*` (upstream xcaddy uses `XCADDY_*`).

| Variable | Effect |
|----------|--------|
| `PORTAL_VERSION` | Default Portal core version to build. |
| `XPORTAL_RACE_DETECTOR` | `1` = pass `-race` (forces cgo on). |
| `XPORTAL_SKIP_BUILD` | `1` = prep environment only, don't compile. |
| `XPORTAL_SKIP_CLEANUP` | `1` = keep the temp build folder. |
| `XPORTAL_DEBUG` | `1` = keep DWARF/source info (`-gcflags all=-N -l`), run `go mod vendor` for dlv. |
| `XPORTAL_GO_BUILD_FLAGS` | When set, replaces the default build flags (signals xportal to skip `-w -s`/`-trimpath`/`-tags nobadger`); flags are inserted after the `go` subcommand. |
| `XPORTAL_GO_BUILD_FLAGS_EXTRA` | Extra flags appended to the final `go build` command. Always applied on top of either the defaults or `XPORTAL_GO_BUILD_FLAGS`. LumeWeb-specific. |
| `XPORTAL_GO_MOD_FLAGS` | Extra flags for `go mod` / `go generate`. |
| `XPORTAL_DISABLE_CGO` | `1` = force `CGO_ENABLED=0`. |
| `XPORTAL_SETCAP` / `XPORTAL_SUDO` | `setcap cap_net_bind_service=+ep` on the produced binary. |
| `XPORTAL_WHICH_GO` | Override the `go` executable (defaults to `go` from `PATH`). |

## Divergence from upstream (`caddyserver/xcaddy`)

xportal is a fork that has intentionally diverged. Key differences:

1. **Target module.** Builds the Portal core (`go.lumeweb.com/portal`) instead of Caddy.
   The default module path is `go.lumeweb.com/portal` (`defaultPortalModulePath` in
   `builder.go`).
2. **Generated `main.go` template** (`environment.go`, `mainModuleTemplate`):
   imports `cmd/portal_embed` and `service` from the Portal core plus `_ "time/tzdata"`,
   rather than Caddy's `cmd`.
3. **Build metadata for core + plugins.** Injects `version`/`gitCommit`/`gitBranch`/
   `buildTime`/`goVersion`/`platform`/`architecture` for the core **and each plugin** via
   `-ldflags -X`. This is a LumeWeb-specific feature **not present in upstream xcaddy**
   (see `builder.go` `Build()` and `environment.go` `getModuleInfo`).
4. **Scratch mode.** `scratch <path>` generates the full environment in a user-specified
   directory and stops — an upstream-independent feature.
5. **Global `go generate`.** xportal runs `go generate ./...` on the whole plugin set
   (upstream only builds Caddy).
6. **Default replacements.** `git.apache.org/thrift.git` → `github.com/apache/thrift` and
   historically `ugorji/go/codec`, `gocron`, and `mapstructure` pins (most now removed).
7. **Env-var rename** `XCADDY_*` → `XPORTAL_*`, plus Portal-specific knobs
   (`XPORTAL_DISABLE_CGO`, `XPORTAL_GO_BUILD_FLAGS_EXTRA`).
8. **Windows resources** via `goversioninfo` (icon/version embedding) — mirrors upstream
   #184 but with Portal branding (`internal/utils/resource.go`).
9. **Protobuf conflict policy.** Injects
   `-ldflags -X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn`
   at build time (`builder.go`) and sets `GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn`
   when running the built binary (`cmd/commands.go`) to tolerate duplicate `rpc.proto`
   registrations.

## Backports from upstream

Explicitly imported changes (older first). "Local commit" = hash in this repo;
"upstream" = hash/PR in `caddyserver/xcaddy`.

| Local commit/file | Upstream source | Notes |
|-------------------|-----------------|-------|
| `0344216` | Cobra CLI integration (upstream **PR #174**, `7888727a`, part of **v0.4.3**) | Migrated the CLI to `spf13/cobra`. |
| `b986980` | `ef415862…` — "Remove deprecated `-d` flag in `go get` (Go 1.23)" (**v0.4.3**) | Drop `-d` from go-get commands. |
| `25443c5` | `7c211675…` (upstream **PR #198**) — "embed: turn source path to absolute for error-less copy" | Uses absolute source paths when copying embedded files. |
| `environment.go` (main template) | `628bcda9…` — **PR #287** (v0.4.7) — "Built binaries do not contain tzdata" | Added `_ "time/tzdata"` import so produced binaries carry the timezone database. |
| `environment.go` (`newGoBuildCommand`) | `33391103…` — **PR #223** (v0.4.5) — "ensure build flags are inserted before arguments" | `XPORTAL_GO_BUILD_FLAGS` are now inserted after the `go` subcommand and *before* positional args. |
| `environment.go` (plugin loop) | `471f043c…` — **PR #238** (v0.4.5) — lexical submodule fix | Replacement prefix check now uses `HasPrefix(path, repl+"/")` so a `foo` replace no longer swallows a distinct `foo-x` module. |
| `builder.go` (Windows version) | `c0aca26d…` — **PR #216** (v0.4.5) — correct version when `--with` replaces core | Truncates `=> replacement` from `go list -m` output before Windows resource embedding. |

## Known gaps vs. current upstream (not yet backported)

These upstream features/fixes have **not** been ported:

- `--embed` — embed a local directory/file tree into the binary (upstream **PR #160**, v0.4.0).
- PGO profile support (`PgoProfile`, `-pgo=` flag) (**PR #259**, v0.4.6).
- Per-step, streaming build output + step callbacks exposed to API consumers
  (**PR #276**, v0.4.7).
- `XCADDY_PRINT_VERSION` env var (**PR #271**, v0.4.7).
- Version string settable via `-ldflags` (**PR #262**, v0.4.7).
- Default build tags: upstream uses `-tags nobadger,nomysql,nopgx`; xportal currently uses
  only `nobadger`.

## Versioning & history notes

- Module path: `go.lumeweb.com/xportal`.
- Release cadence uses **Knope** (migrated from Changesets in `4982ee4`).
- Config uses **`go-viper/mapstructure/v2`**; the old `mitchellh/mapstructure` replace
  directive was dropped in v0.2.15.

## Conventions

- Config/plugin conventions follow Go; keep exported API in the root package
  (`xportal.Builder`, `xportal.Dependency`, `xportal.Replace`) stable for library users.
- New upstream features are brought in as explicit backport commits citing the upstream
  hash/PR (see Backports section) — prefer that over re-writing from scratch.
- Keep env vars namespaced `XPORTAL_*` and documented in the table above.
