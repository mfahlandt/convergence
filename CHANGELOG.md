# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `convergence skills scan` and `skills build`: vet agent skills with NVIDIA SkillSpector (pinned v2.13.4) in a container. Static by default (`--network=none`); `--llm` adds semantic analysis through the gateway with a model that honors forced tool calls (`skill_scan_model`).
- Opt-in: `sandbox run` checks installed skills first with `skill_scan: warn` or `block` (default `off`), reusing the cached scan until the skills change. `doctor` reports the cached result.
- `insights` reports total cost, cost per tier (with an "N of M rows priced" coverage line) and estimated savings versus a reference model (`--reference-model`, default: the heavy-tier model), also in `--json`. Prices are pinball's optimizer catalog estimates, not invoices.
- `convergence doctor` warns when a model that `models.yaml` maps to Ollama is not installed on the configured endpoint. README documents mapping the light tier to a local Ollama model.
- `cluster install --request-log-retention DUR` renders a daily CronJob that deletes request-log rows older than DUR in batches (off by default, minimum 24h, the optimizer's sampling window).
- `sandbox run hermes`: Hermes ignores `OPENAI_BASE_URL`, so Convergence runs `hermes chat` with `--provider custom --base-url <inference URL> --api-key convergence`. Other Hermes subcommands are passed through.
- `convergence config openclaw` adds a `convergence` provider (models `auto`, `light`, `standard`, `heavy`) to `~/.openclaw/openclaw.json`; `--set-default` also sets `agents.defaults.model.primary`.
- `convergence config hermes --set-default` makes `convergence` / `auto` the Hermes default model (`model.provider`, `model.default`, `model.base_url`). Without the flag the default stays untouched.

### Fixed

- `config hermes` writes to Hermes' real config: `$HERMES_HOME`, else `%LOCALAPPDATA%\hermes` on Windows, else `~/.hermes`. It always used `~/.hermes`, which Hermes does not read on Windows or with `HERMES_HOME` set.

## [0.3.1] - 2026-11-10

### Added

- `cluster backup --out DIR` and `cluster restore --from FILE [--yes]` for the request-log Postgres (and pinball overlay). `cluster install` backs up an existing request-log Postgres first (last 5 kept in `~/.convergence/backups/`, `--no-backup` skips).

### Fixed

- `cluster install` re-runs no longer rotate the Stand-In salt, replace `GH_COPILOT_TOKEN` with an empty value or generate a new request-log Postgres password: omitted secrets are read back from the Secrets of the previous install (`stand-in`, `convergence-copilot`, `pinball-optimizer-db`). `--provider copilot` now fails early when no token is given and none exists in the cluster.

## [0.3.0] - 2026-11-10

### Added

- `cluster install --standin-cpu-request` / `--standin-cpu-limit` set Stand-In's CPU (defaults `50m` / `500m`, unchanged). Raise the limit if ext_proc times out on large request bodies.
- agentgateway now records `pinball.score` and `pinball.est_tokens` from Pinball's `/check` in the access/request log (compose and Kubernetes), so routing thresholds can be tuned from real traffic.

### Fixed

- Pinball optimizer could never replay candidates: it sends them as `opt-<pinball model>` (`PINBALL_OPT_MODEL_PREFIX`), but only the internal `pinball-<model>` aliases were rendered, so every candidate call was a 404. The gateway config (local and cluster) now renders public `opt-` twins when the request log is configured. The CronJob and `convergence optimize` also set `PINBALL_OPT_JUDGES=heavy`; pinball's default judges (`judge-opus`, `judge-argon`) are aliases Convergence never renders.

## [0.2.5] - 2026-11-09

### Changed

- Bump pinned Stand-In image to `0.6.2` (`ghcr.io/jeefy/stand-in:0.6.2@sha256:846044102ae1990e924bf43f345addaaa63a5a387c12ad26cb0393f98716d414`).
- Bump pinned Pinball image to `0.7.6` (`ghcr.io/jeefy/pinball:0.7.6@sha256:c34b38b977b057c3a05e4309e68b8ced10da019b377a267e3f740909f6de2fee`).

### Added

- `--extproc-timeout` flag for `init` and `cluster install` (config field `extproc_timeout`) to configure the ext_proc `requestTimeout` (default `5s`, unchanged).

### Fixed

- Empty `request_logs.cost`: the `copilot-completions` provider used `providerOverride: openai`, so agentgateway looked pricing up under `openai/claude-*` (no match). It now uses the `copilot` catalog key.
- OpenShell on Podman: discover and mount the Podman API socket and select OpenShell's Podman driver. On Linux, the OpenShell gateway disables SELinux labeling to access the socket.
- Podman startup: detect `nft` builds without JSON support before Compose starts and stream Compose startup output.

## [0.2.4] - 2026-11-08

### Changed

- Bump pinned Pinball image to `0.7.5` (`ghcr.io/jeefy/pinball:0.7.5@sha256:3e70723ef85ba4f6d80d2f2ece790263f6e7819880c7b4e59ed35b3cbb8b7b17`).

## [0.2.3] - 2026-11-08

### Changed

- Bump pinned Stand-In image to `0.6.1` (`ghcr.io/jeefy/stand-in:0.6.1@sha256:baa7bd9bb4de581114ba9e58ecc36355703a742b2170e73d8324d61f09134a3e`).

### Added

- `init --compose-provider auto|docker-compose|podman-compose`: persist the Podman Compose provider in `config.yaml` for lifecycle commands and `doctor`. Empty or `auto` preserves Podman's default selection; explicit choices override `PODMAN_COMPOSE_PROVIDER` and require the provider on `PATH`. Docker Engine is unchanged.

### Fixed

- Podman: read Compose service names from the `Labels` map instead of the newer `Label` template helper, fixing running-service checks on older distro releases such as Ubuntu 24.04's Podman.

## [0.2.2] - 2026-11-08

### Added

- `convergence insights`: analyze 1d, 7d, or 30d of prompt, completion, token usage, and Pinball tier allocation patterns from agentgateway's Postgres request log (`request_logs` and `request_log_payloads`). Identifies context bloat, heavy-tier over-utilization, and rapid retry loops. Optional `--coach` synthesizes actionable recommendations via the Convergence gateway (`POST /v1/chat/completions`). Supports `--json` for machine-readable output.

## [0.2.1] - 2026-11-08

### Fixed

- Compose: keep the OpenShell gateway under `services` and declare optional named volumes in one mapping, fixing startup with the Copilot provider and OpenShell sandbox enabled together.

## [0.2.0] - 2026-11-08

### Added

- `models.yaml` passthrough entries accept `base_url` and `api_key_env` (env var name) for custom endpoints such as Ollama Cloud; one gateway provider per distinct endpoint (local and cluster). Local keys go to the 0600 `.env`; cluster keys come from a user-created Secret (`cluster install --passthrough-secret`, default `convergence-passthrough`). Cluster installs now allow `ollama` passthrough when `base_url` is set.
- `init --standin-patterns-file` and `cluster install --standin-patterns-file`: use a custom Stand-In `patterns.yaml` (compose bind mount / ConfigMap `stand-in-patterns`, plus `STANDIN_PATTERNS_FILE`). Replaces the image default; unset is unchanged.
- `models.yaml`: optional `passthrough:` block (`name: {provider: copilot|openai|anthropic|ollama, model: id}`) exposes fixed model names that bypass Pinball (public `llm.models` entries; local and cluster). Names colliding with `auto`/`pinball`/`light`/`standard`/`heavy`/`pinball-*`, unknown providers and empty models are errors. Stand-In still scrubs every request.
- `cluster install --request-log-postgres` (in-cluster Postgres with generated password) or `--request-log-db <dsn>`: agentgateway writes its request log to Postgres with LLM logging `full` (stores prompts and completions after Stand-In scrubbing) and the optimizer CronJob reads the same Secret `pinball-optimizer-db` via `PINBALL_OPT_DB_DSN`. Off by default; the DSN is never rendered into a ConfigMap. New `--request-log-postgres-password`.
- agentgateway now returns Pinball's pick on every `auto`/`pinball` response as headers `x-pinball-model`, `x-pinball-tier` and `x-pinball-reason` (a `transformations.response` policy reading ext_authz metadata; local and cluster). The body's `model` still names the real upstream model, so clients that compare it to the requested `auto` can read the header instead of reporting a route change.
- `models.yaml`: optional `pinball:` block (`thresholds`, `scoring`) overrides Pinball's score cutoffs and per-signal points, rendered into `pinball.yaml` (local and cluster ConfigMap). Defaults unchanged; unknown keys and invalid cutoff order are errors.

### Fixed

- Podman: detect running services with native `podman ps` and Compose labels instead of Docker-only `compose ps --services --filter` flags. Running-service check failures now include stderr.
- copilot-api: fold `max_completion_tokens` into `max_tokens` (same preloaded shim). copilot-api fills in `max_tokens` when it is missing, so any request carrying `max_completion_tokens` (the current OpenAI parameter, sent by default by clients like OpenClaw) reached Copilot with both and failed with 400 `max_tokens and max_completion_tokens cannot both be set`.

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

- Optional request-log database for local `convergence optimize`: `init --request-log-db <postgres-dsn>` (config `request_log_db`) renders `config.logging.database` plus `frontendPolicies.accessLog.database.llm: full` in the compose config; the DSN is passed via `.env`. `convergence optimize` falls back to it when `--db-dsn` is not given. Off by default. `full` stores prompts and completions.
- agentgateway `frontendPolicies.accessLog.add` now records `pinball.model`, `pinball.tier` and `pinball.reason` (null when the request was not routed by Pinball), in the compose and Kubernetes configs. `pinball-optimize` filters on these attributes in the request log.
- Optional OTLP trace export: `otel.endpoint` (+ `protocol` grpc|http, `sample`) in config, `--otel-endpoint/--otel-protocol/--otel-sample` on `init` and `cluster install`. Renders `frontendPolicies.tracing` (service `convergence`, attributes `caller`, `pinball.tier|model|reason`) for compose and Kubernetes. Unset = rendered files unchanged.
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

[Unreleased]: https://github.com/jeefy/convergence/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/jeefy/convergence/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/jeefy/convergence/compare/v0.2.5...v0.3.0
[0.2.2]: https://github.com/jeefy/convergence/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/jeefy/convergence/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jeefy/convergence/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/jeefy/convergence/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/jeefy/convergence/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/jeefy/convergence/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/jeefy/convergence/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/jeefy/convergence/releases/tag/v0.1.0
