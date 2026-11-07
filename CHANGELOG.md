# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `models.yaml`: optional `pinball:` block (`thresholds`, `scoring`) overrides Pinball's score cutoffs and per-signal points, rendered into `pinball.yaml` (local and cluster ConfigMap). Defaults unchanged; unknown keys and invalid cutoff order are errors.

## [0.1.4] - 2026-11-06

### Changed

- Updated pinned component images:
  - `ghcr.io/jeefy/stand-in` to `0.6.0` (digest `sha256:39db3c7318d49b9e6d2f7bde7ec2c7828522437dddbc02b0a1bd9c07b391c2b2`)
  - `ghcr.io/jeefy/pinball` to `0.7.4` (digest `sha256:0f70fd86a0b1fd1926909df11bfa6d9c388568d01a107c842677af3417be9b4d`)

### Fixed

- copilot-api: disable Bun's 10s `idleTimeout` via a preloaded shim (compose + Kubernetes). Long or silent upstream completions were cut mid-response (`request timed out after 10 seconds`) and agentgateway then waited on a dead upstream. Upstream issue: ericc-ch/copilot-api#153.
- compose: update stand-in service dependency and remove python3 healthcheck for distroless Go container.

## [0.1.3] - 2026-11-05

### Added

- Model tier routes: virtual models `light`, `standard`, and `heavy` exposed directly in gateway configs without requiring `x-pinball-tier` header overrides.
- Client configs: support for Zed (`convergence config zed`) and Cline / Roo Code (`convergence config cline`), plus tier model exports (`convergence/light`, `convergence/standard`, `convergence/heavy`) for OpenCode, Continue, and Hermes.
- Model mapping: support multi-provider mixing in `models.yaml` (e.g. `ollama:` for light tier and `anthropic:` for heavy tier) and dynamic upstream instantiation in gateway templates.
- Kubernetes: `--ingress-host` and `--enable-pdb` flags on `convergence cluster install` for optional Ingress and PodDisruptionBudgets, plus `convergence cluster logs` command to stream in-cluster pod logs.
- Security: image SHA256 digest pinning for immutable releases across agentgateway, Stand-In, Pinball, and copilot-api.
- Lifecycle & compose cleanup: added `convergence down` and `convergence prune` commands for local Docker/Podman compose environments.
- Observability: latency quantiles (P50, P90, P99) in `convergence stats` and bundled sample Grafana dashboard in `docs/grafana-dashboard.json`.
- Sandboxing: `-v` / `--mount` flag for `convergence sandbox run` to pass workspace directories into nono, OpenShell, and Docker Sandboxes (`sbx`).

### Fixed

- Test suite: podman smoke test no longer requires a live OpenShell docker socket.

## [0.1.2] - 2026-11-05

### Added

- Pluggable agent sandboxing runtime support (`internal/sandbox`): `none`, `openshell`, `nono`, `docker-sbx`, and `k8s-agent`.
- `nono` driver for kernel-enforced process sandboxing (Landlock on Linux, Seatbelt on macOS) with ~0ms latency and zero background daemons.
- `convergence sandbox run <agent> [args...]` command to launch agents directly inside the configured sandbox with inference pre-routed to Convergence.
- `convergence init --sandbox <runtime>` flag to choose or enable sandbox integration.
- Optional NVIDIA OpenShell gateway compose service bundled with schema v2 `openshell.toml` config, exposing ports 17670 (gRPC/HTTP) and 17671 (healthz).
- `convergence sandbox status` and `convergence sandbox list` CLI commands.

## [0.1.1] - 2026-11-05

### Added

- Component image overrides: `--image-agentgateway`, `--image-standin`, `--image-pinball`, and `--image-copilot-api` flags for `init` and `cluster install` (persisted in `config.yaml` for local compose), allowing deployment of specific image tags, dev SHAs, or custom registries.

### Fixed

- Kubernetes mode: agentgateway is started with `--file` instead of `-f`; with `-f` v1.6.0 exits in-cluster (`configuration is required when running in Kubernetes`).

## [0.1.0] - 2026-11-05

### Added

- Local stack on Docker or Podman compose: agentgateway `v1.6.0`, Stand-In `0.4.1`, Pinball `0.7.3`, and copilot-api `v0.7.0` for the Copilot provider.
- Providers: `copilot`, `direct` (OpenAI and Anthropic keys) and `ollama`.
- `init` with interactive prompts or flags (`--yes`, `--provider`, `--engine`, `--copilot-token`, `--openai-key`, `--anthropic-key`, `--ollama-url`, `--ollama-model-light/-standard/-heavy`, `--bind-address`, `--port`, `--fail-open`).
- `login copilot` (GitHub device flow).
- Lifecycle commands: `start`, `stop`, `restart`, `status`, `logs [-f] [service]`, `upgrade`, `uninstall [--purge] [--yes]`.
- Stand-In is fail-closed by default (requests are rejected when it's down). `init --fail-open` and `cluster install --fail-open` opt into unscrubbed passthrough. Pinball ext_authz stays `allow` and falls back to the standard-tier model.
- `doctor [--e2e] [--json]` self-diagnosis, and `status [--json]` per-service probes with loud warnings when Stand-In is down or fail-open.
- Model mapping: `models`, `models init`, `~/.convergence/models.yaml` overrides, `cluster install --models-file`, per-tier Ollama flags, and a missing-model warning at `start` (`start --strict` to fail).
- Direct providers: OpenAI and Anthropic. Gemini isn't supported.
- Compose hardening: `json-file` log rotation, healthchecks for Stand-In, Pinball and copilot-api, secrets in a `0600` `.env`, loopback-only port binding.
- Kubernetes: the Stand-In Service exposes metrics on 9102.
- Dev: `make lint` (golangci-lint, gofmt, vet), race-detector tests and a coverage summary in CI; the local e2e sends a real chat through a stub upstream and the KinD e2e does a real chat plus a fail-closed check.
- `stats [--json] [--watch]` built from the Prometheus metrics of the gateway, Stand-In and Pinball.
- Client configuration with backup and restore: OpenCode, oh-my-opencode, Claude Code, Aider, Goose, Hermes Agent, Continue, plus `config copilot` shell exports. `config list`, `--dry-run`, `--gateway-url`, `restore --force`.
- Kubernetes mode: `cluster install`, `cluster status`, `cluster uninstall`, with NetworkPolicies and an optional weekly Pinball optimizer CronJob.
- Experimental `optimize --db-dsn` for on-demand Pinball evaluation.
- Local ports bind to `127.0.0.1` by default (`bind_address`); config and secret files are written `0600`.
- Installer with sha256 verification against `checksums.txt`, version pinning and no `sudo`.
- Release builds for linux and darwin on amd64 and arm64, with checksums and SBOMs.

### Known limitations

- Windows isn't supported. WSL2 works.
- The Pinball optimizer is experimental and opt-in.
- Container images are pinned by tag, not by digest.
- The model ids in the default mapping are defaults; providers rename models, so override them in `models.yaml` if a request fails with an unknown model.
- The gateway has no authentication of its own: any local process can use your keys via `127.0.0.1:3000`, and in Kubernetes any pod the NetworkPolicy allows can.
- Fail-closed means a Stand-In outage rejects requests.

[Unreleased]: https://github.com/jeefy/convergence/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/jeefy/convergence/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/jeefy/convergence/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/jeefy/convergence/releases/tag/v0.1.0
