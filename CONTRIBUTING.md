# Contributing

Thanks for helping. Bug reports, fixes and new client integrations are all welcome. By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Dev setup

You need Go (version in `go.mod`), make, and Docker or Podman with compose for the end-to-end tests. Cluster e2e also needs `kubectl` and a disposable cluster (kind works).

```bash
make build              # bin/convergence
make test               # go test ./...
make lint               # golangci-lint, go vet, gofmt check
make test-e2e           # local compose stack, tests/test_e2e_local.sh
make test-e2e-cluster   # Kubernetes, tests/test_e2e_cluster.sh
bash tests/test_install.sh   # installer tests, offline
go test -race -count=1 ./...   # what CI runs
```

CI also runs `golangci-lint` (config in `.golangci.yml`) and prints a coverage summary. `make fmt` formats the tree.

What the e2e tests do:

- `tests/test_e2e_local.sh` brings up the real compose stack with the Ollama provider pointed at a stub upstream (`tests/stub-upstream/`), then sends real chat requests (plain and streaming) through the gateway.
- `tests/test_e2e_cluster.sh` installs into a disposable cluster (KinD in CI), sends a real chat through the gateway, and then checks fail-closed behavior with Stand-In down.
- `cluster install` has a hidden `--openai-base-url` flag (provider `direct`) that points the OpenAI backend at a stub or self-hosted endpoint. It's for tests only and isn't a supported user option.

## Layout

```
cmd/convergence/        main package
internal/cli/           cobra commands
internal/config/        ~/.convergence/config.yaml model and defaults
internal/models/        tier to upstream model mapping and models.yaml overrides
internal/doctor/        checks behind `convergence doctor`
internal/health/        service probes behind `status`
internal/templates/     compose, agentgateway and pinball templates (local mode)
internal/k8s/           cluster manifests and installer
internal/clientconfig/  per-client config writers with backup/restore
internal/telemetry/     metrics scraping for `stats`
internal/version/       build metadata (set via ldflags)
tests/                  shell e2e and installer tests
docs/                   architecture notes
```

## Adding a client config

1. In `internal/clientconfig/`, add a path helper for the client's config file and a configure function. Write a `.convergence.bak` backup before changing anything, and reuse the shared restore helper.
2. Add tests in `clientconfig_test.go` that run against a temp `HOME`: configure, restore, and restore when no backup exists.
3. Register the subcommand in `internal/cli/clientconfig.go` so it gets `--dry-run`, `--gateway-url` and `restore [--force]`, and shows up in `config list`.
4. Add a row to the client table in `README.md` and an entry under `[Unreleased]` in `CHANGELOG.md`.

## Bumping pinned image versions

Image tags appear in several places. Change all of them in one PR:

```bash
grep -rn 'agentgateway:v\|stand-in:\|pinball:\|copilot-api:v\|skillspector' --include='*.go' --include='*.tmpl' --include='*.sh' --include='*.md' .
```

That covers `internal/templates/render.go`, `internal/k8s/assets/manifests.yaml.tmpl`, `internal/cli/optimize.go`, `tests/`, `README.md` and `docs/architecture.md`. SkillSpector is built from source, so its pin is a commit SHA plus tag (`SourceCommit`/`DefaultImage` in `internal/skillscan/skillscan.go`, mirrored in `tests/test_e2e_local.sh` and `README.md`). Read the upstream changelog (agentgateway config schema changes are the usual breakage), run `make test-e2e` and `make test-e2e-cluster`, and note the bump in `CHANGELOG.md`.

## Pull requests

- Keep changes focused; one topic per PR.
- `make test` and `make lint` must pass.
- Anything that drives a container engine or the stack (new subcommand, flag, config key) needs coverage in `tests/test_e2e_local.sh` (runs in CI on Docker and both Podman compose providers) or `tests/test_e2e_cluster.sh`. Unit tests with a fake engine are not a substitute.
- Don't commit secrets or real tokens in tests or fixtures.

## Releases

Maintainers tag `vX.Y.Z` on `main`. The release workflow runs the tests and GoReleaser, which publishes archives, `checksums.txt` and SBOMs. Check the config locally with:

```bash
go run github.com/goreleaser/goreleaser/v2@v2.6.1 check
go run github.com/goreleaser/goreleaser/v2@v2.6.1 release --snapshot --clean --skip=publish,sbom
```
