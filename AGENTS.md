# AGENTS.md

Guidance for AI coding agents working in this repo. Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md). Facts here were learned the hard way; verify before changing them.

## What this is

Convergence is a Go (1.24, cobra) CLI that deploys and manages three upstream services as one local (docker/podman compose) or Kubernetes stack: **agentgateway** (gateway), **Stand-In** (`ghcr.io/jeefy/stand-in`, credential tokenizing via ext_proc) and **Pinball** (`ghcr.io/jeefy/pinball`, tier routing via ext_authz). It also writes client configs (`config <client>`). Layout is in CONTRIBUTING.md; request flow is in `docs/architecture.md`.

## Commands

```bash
make build && make test        # unit tests
make lint                      # golangci-lint + go vet + gofmt; must be 0 issues
go test -race -count=1 ./...   # what CI runs
make test-e2e                  # real compose stack + stub upstream (needs docker or podman)
make test-e2e-cluster          # real KinD cluster (kind + kubectl)
bash tests/test_install.sh     # offline installer tests
```

Run `make lint` and `go test -race ./...` before declaring work done. Never commit, push or tag unless asked.

## Rules that are easy to get wrong

- **Single source of truth for models:** tier to upstream mapping lives in `internal/models`. Both the local templates and `internal/k8s` render pinball.yaml and agentgateway `virtualModels` from `models.Resolve`. Do not hardcode model names in `*.tmpl`; a test enforces this.
- **Stand-In is fail-closed by default** (`failureMode: failClosed`). `--fail-open` is the explicit opt-out on both `init` and `cluster install`. Pinball ext_authz stays `allow` on purpose (it only chooses a model). Do not flip these defaults.
- **Secrets never go in mounted config files.** Local: API keys, Copilot token and the Stand-In salt live in `~/.convergence/.env` (0600) and reach containers via compose `${VAR}`. agentgateway reads `$VAR` from its environment. Rendered `agentgateway.yaml`, `pinball.yaml` and `docker-compose.yaml` are 0644 so non-root containers can read them. K8s: the agentgateway config is a Secret, not a ConfigMap.
- **agentgateway expands `$VAR` everywhere in its config, including YAML comments,** and refuses to start if a name is unset. Never write a literal `$` in `agentgateway.yaml.tmpl` comments; a unit test guards this.
- **Kubernetes containers use `args:`, not `command:`,** to pass `-f cfg` to agentgateway. `command` replaces the image ENTRYPOINT and the pod never starts (this broke CI once). A test guards it.
- **Pinball 0.7.6 config schema:** `models: {name: {prompt_tokens}}`, `tiers: {light, standard, heavy}`, `thresholds: {standard, heavy}`. Other keys (`deep`, `cost_per_m_*`, `scoring`) crash the container. Validate by running the image against the rendered file.
- **K8s pods need `enableServiceLinks: false`:** the `pinball` Service injects `PINBALL_PORT=tcp://...`, which crashes pinball.
- **Pinned images** (change all places together, see CONTRIBUTING): agentgateway `v1.6.0`, stand-in `0.6.2`, pinball `0.7.6`, copilot-api `v0.7.0` (reads `GH_TOKEN`; `v0.1.25` never existed).
- **No cost estimates anywhere.** `stats` reads Prometheus metrics (agentgateway :15020, Stand-In :9102, Pinball :9201). Do not fabricate numbers.
- **Client config writers** must keep user comments and other keys (JSONC-safe text edits, `yaml.Node` for YAML), write atomically, back up to `<file>.convergence.bak` and restore safely. Only unverified-against-docs keys are bugs: check the client's docs before adding or changing one. Cursor has no file-based override and is intentionally unsupported.
- The optimizer (`convergence optimize`, k8s CronJob) is experimental and opt-in. It needs agentgateway's Postgres request log (`PINBALL_OPT_DB_DSN`); the local stack ships no Postgres (`init --request-log-db` points agentgateway at yours, logging `full` = prompts stored).

## Testing notes

- **Every engine-driving change needs real e2e coverage.** A new subcommand, flag or config key that talks to docker/podman, the compose stack or a cluster must be exercised in `tests/test_e2e_local.sh` (CI runs it on Docker, Podman+docker-compose and Podman+podman-compose) or `tests/test_e2e_cluster.sh`, with assertions on exit codes and output. Unit tests with a fake engine do not count. Run the script locally before pushing; "tested on my machine with podman" in a PR description is not coverage.

- e2e scripts use fixed host ports (3000, 9102, 9201, 15020). Check `podman ps -a` / `docker ps` for leftovers and do not run two e2e runs at once.
- Rootless podman: bind mounts need `:ro,z` on Linux; `podman compose` delegates to docker-compose and needs the podman API socket (the e2e script starts one).
- Unit tests must not touch the network or a real container engine: use a fake engine script on `PATH`, `t.Setenv("HOME", t.TempDir())` and `httptest`.
- Test fixtures use fake tokens only (`ghp_` plus 36 alphanumerics); never real credentials.
- Lint exclusions in `.golangci.yml` are narrow and commented. Fix findings rather than widening them; no blanket `nolint`.

## Style

- Errors: wrap with `%w`, one `Error:` line printed once (handled in `cmd/convergence/main.go`). Never swallow an error that matters.
- Minimal changes; match the surrounding code. Don't add dependencies without need (the Prometheus parser and JSONC editor are deliberately dependency-free).
- Docs: concise and factual, no marketing, no cost-savings claims, no em dashes. Update `README.md`, `docs/architecture.md` and the `[Unreleased]`/v0.1.0 entry in `CHANGELOG.md` when behavior changes.
- `docs/superpowers/` is gitignored local scratch; never commit it.
