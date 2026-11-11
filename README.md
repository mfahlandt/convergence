# Convergence

Convergence runs an AI gateway stack on your workstation or in a Kubernetes cluster and points your coding agents at it. It wires together several projects:

- [agentgateway](https://agentgateway.dev/docs/) (`v1.6.0`): the gateway. It serves an OpenAI-compatible `/v1` endpoint (Anthropic Messages requests are translated) and forwards to your provider.
- [Stand-In](https://github.com/jeefy/stand-in) (`0.6.2`): an `ext_proc` filter that swaps secrets and credentials in prompts for opaque tokens before they leave your machine, then puts the originals back in responses.
- [Pinball](https://github.com/jeefy/pinball) (`0.7.6`): an `ext_authz` service that scores each sanitized request and picks a model tier (`light`, `standard`, `heavy`).
- **Bring Your Own Sandbox**: run your agents inside an isolated runtime while funneling model traffic through the stack. Supported options include [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) (bundled compose gateway container and supervisor), [nono](https://github.com/nolabs-ai/nono) (zero-daemon kernel isolation via Landlock/Seatbelt), [Docker Sandboxes](https://docs.docker.com/engine/security/sandboxes/) (`sbx`), and Kubernetes [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) (`agents.x-k8s.io`).

Why: you get one endpoint for every agent, secrets scrubbed on the wire, and per-request model routing, without hand-writing gateway config.

```
 Client (OpenCode, Claude Code, Aider, Goose, ...)
 ┌────────────────────────────────────────────────────────┐
 │ [Optional Sandbox: OpenShell / nono / sbx / k8s-agent] │
 └───────────────────────────┬────────────────────────────┘
                             │ HTTP 127.0.0.1:3000 (/v1)
                             ▼
 ┌─────────────────── agentgateway ───────────────────┐
 │ 1. ext_proc ──► Stand-In   tokenize secrets        │
 │ 2. ext_authz ─► Pinball    choose model tier       │
 │ 3. route ─────► upstream                           │
 │ 4. ext_proc ──► Stand-In   restore secrets         │
 └───────────────────────────┬────────────────────────┘
                             │
                             ▼
 copilot-api :4141 (Copilot) │ OpenAI / Anthropic (direct) │ Ollama
```

More detail, including ports and on-disk layout: [docs/architecture.md](docs/architecture.md).

## Prerequisites

- Linux or macOS on amd64 or arm64. Windows isn't supported natively; use WSL2 (see [Windows](#windows-wsl2)).
- For the local stack: Docker with the compose plugin, or Podman with `podman-compose` (or `podman compose`).
- For the cluster mode: `kubectl` access to a cluster.
- Credentials for one provider (see [Providers](#providers)).

## Windows (WSL2)

Run Convergence inside a WSL2 distro: install it there like on Linux and keep Docker or Podman reachable from that distro. `convergence config <client>` edits files in the distro's home (`~`), not in your Windows profile, so it configures clients that run inside WSL2. For a client on the Windows side, point it at `http://localhost:3000/v1` (API key `convergence`) yourself, or use `--gateway-url` with `--dry-run` to see the values first. Native Windows builds are not tested: several unit tests fail there because they assume a Unix home and file modes, and the tests that set `HOME` write to the real `%USERPROFILE%`.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/jeefy/convergence/main/install.sh | bash
```

The installer:

- downloads `convergence_<os>_<arch>.tar.gz` and `checksums.txt` from the GitHub release and refuses to install if the sha256 doesn't match;
- installs to `/usr/local/bin` if writable, otherwise `~/.local/bin`. It never runs `sudo`;
- checks for docker/podman with compose and prints guidance if missing.

Options:

```bash
CONVERGENCE_VERSION=v0.1.0 bash install.sh   # pin a release (default: latest)
INSTALL_DIR=$HOME/bin bash install.sh        # choose the target directory
```

From source (Go 1.24+):

```bash
git clone https://github.com/jeefy/convergence.git && cd convergence
make build            # produces bin/convergence
```

## Quickstart

### Local

```bash
convergence login copilot     # only for the Copilot provider
convergence init              # pick engine, provider, and optional sandbox; writes ~/.convergence
convergence start
convergence status
convergence config opencode   # or claude-code, aider, goose, ...
```

Non-interactive:

```bash
convergence init --yes --engine podman --provider direct \
  --openai-key "$OPENAI_API_KEY" --anthropic-key "$ANTHROPIC_API_KEY" --port 3000
```

For Podman, `init --engine podman --compose-provider podman-compose` selects the daemonless Compose provider. `--compose-provider docker-compose` selects Docker Compose talking to Podman's API socket. The choice is saved as `compose_provider` in `config.yaml` and used by lifecycle commands and `doctor`. Omitted flags keep the saved choice; `--compose-provider auto` restores Podman's default selection, including `PODMAN_COMPOSE_PROVIDER` and `containers.conf`. Explicit choices override that environment variable and must be installed on `PATH`. Docker Engine is unchanged.

With an isolated sandbox:

```bash
convergence init --yes --provider direct --sandbox nono
convergence start
convergence sandbox run opencode
```

`sandbox run hermes` works too. Hermes ignores `OPENAI_BASE_URL`, so Convergence runs `hermes chat --provider custom --base-url <gateway> --api-key convergence` for a bare `hermes`, `hermes chat` or leading flags. Other Hermes subcommands are passed through unchanged.

For the bundled OpenShell gateway, use `--sandbox openshell` with any provider, including Copilot. With Podman, enable its API socket first (`systemctl --user enable --now podman.socket` on rootless Linux). Convergence mounts the discovered socket and selects OpenShell's Podman driver. On Linux, SELinux labeling is disabled for the OpenShell gateway container so it can access the socket; access to the socket grants control over the user's Podman containers.

### Kubernetes

```bash
convergence cluster install --namespace convergence --provider copilot --copilot-token "$GH_TOKEN"
convergence cluster status --namespace convergence
kubectl -n convergence port-forward svc/agentgateway 3000:3000
convergence config opencode --gateway-url http://127.0.0.1:3000
```

## Providers

| Provider | Upstream | Credentials |
|---|---|---|
| `copilot` | GitHub Copilot through [copilot-api](https://github.com/ericc-ch/copilot-api) (`v0.7.0`) on `:4141` | A GitHub account with an active Copilot subscription. Run `convergence login copilot` (device flow) or pass `--copilot-token`. |
| `direct` | OpenAI and Anthropic APIs (the only direct families; Gemini isn't supported) | `--openai-key` from <https://platform.openai.com/api-keys>, `--anthropic-key` from <https://console.anthropic.com/settings/keys>. Either or both. |
| `ollama` | A local Ollama server | No key. `--ollama-url` defaults to `http://host.docker.internal:11434`. Local mode only. |

Cluster mode supports `copilot` and `direct`.

### Model mapping

Pinball scores each request into a tier (`light`, `standard`, `heavy`). Convergence maps each tier to one pinball model, and each pinball model to an upstream model for your provider. Show the active mapping with `convergence models`:

```
Provider: direct
TIER      PINBALL MODEL      UPSTREAM
light     claude-haiku-5.5   anthropic/claude-haiku-5-5
standard  claude-sonnet-5.5  anthropic/claude-sonnet-5-5
heavy     claude-opus-5.5    anthropic/claude-opus-5-5
```

Defaults per provider:

| Tier | Pinball model | `copilot` | `direct` | `ollama` |
|---|---|---|---|---|
| light | `claude-haiku-5.5` | `claude-haiku-5.5` | `anthropic/claude-haiku-5-5` | `qwen2.5-coder:7b` |
| standard | `claude-sonnet-5.5` | `claude-sonnet-5.5` | `anthropic/claude-sonnet-5-5` | `qwen2.5-coder:14b` |
| heavy | `claude-opus-5.5` | `claude-opus-5.5` | `anthropic/claude-opus-5-5` | `qwen2.5-coder:32b` |

These ids are defaults, not guarantees: providers rename and retire models. Override them in `~/.convergence/models.yaml` (`convergence models init` writes a commented starter and never overwrites an existing file):

```yaml
# provider -> pinball model -> upstream model
direct:
  claude-haiku-5.5: openai:gpt-5-mini   # direct accepts anthropic: (default) or openai:
ollama:
  claude-opus-5.5: qwen2.5-coder:14b
```

Only `copilot`, `direct` and `ollama` are valid provider keys, and only the three pinball model names above are valid. An invalid file is an error. Re-run `convergence restart` after editing. For Ollama you can instead set tiers at init with `--ollama-model-light`, `--ollama-model-standard` and `--ollama-model-heavy`; `models.yaml` wins over those flags. In cluster mode pass the same file with `cluster install --models-file models.yaml`.

#### Hybrid: local light tier

Short, simple prompts (titles, one-line questions) are routed to the light tier. You can keep the heavier tiers on your cloud provider and serve only the light tier from a local Ollama model, which costs nothing per request. Use an `ollama:` prefix, which is accepted for `copilot` and `direct` as well:

```yaml
copilot:
  claude-haiku-5.5: ollama:gemma4:26b   # light tier runs locally
```

Set `ollama_url` in `~/.convergence/config.yaml` (default `http://host.docker.internal:11434`), pull the model (`ollama pull gemma4:26b`) and run `convergence restart`. A model in the `gemma4:26b` class has been a good fit for titles and short answers; a 7b-class model is faster but noticeably weaker. Trade-offs: local latency depends on your hardware (the first request after idle loads the model), and quality is below the hosted light model, so keep the standard and heavy tiers on the cloud. `convergence doctor` warns when a model mapped to Ollama is not installed on the endpoint. Check routing with a short request and `model: auto`: the `x-pinball-tier: light` and `x-pinball-model` response headers show where it went.

#### Passthrough models

To call a fixed model by name (no Pinball routing), add a `passthrough:` block. The client uses the key as the model name; Stand-In still scrubs the request and the `x-pinball-*` response headers stay empty.

```yaml
passthrough:
  copilot-fable-5: {provider: copilot, model: claude-fable-5}
  glm-5.2:         {provider: ollama,  model: glm-5.2}
```

`provider` is `copilot`, `openai`, `anthropic` or `ollama`. Names must not be `auto`, `pinball`, `light`, `standard`, `heavy` or start with `pinball-`. On a cluster install, `ollama` needs a `base_url` and `copilot` needs `--provider copilot`.

An entry may set `base_url` and `api_key_env` (the **name** of an env var, never the key) to use another endpoint, e.g. Ollama Cloud. Entries with the same `(provider, base_url, api_key_env)` share one gateway provider; entries without them keep using the shared provider (local Ollama, `--ollama-url`).

```yaml
passthrough:
  glm-5.2: {provider: ollama, model: glm-5.2, base_url: 'https://ollama.com', api_key_env: OLLAMA_CLOUD_KEY}
  gemma4:  {provider: ollama, model: gemma4}   # local Ollama
```

Local: export `OLLAMA_CLOUD_KEY` when running `init`/`restart`/`upgrade`; it is written to the 0600 `.env` (an existing value is kept if the variable is unset) and the config only contains `${OLLAMA_CLOUD_KEY}`. Cluster: create the Secret yourself, e.g. `kubectl -n convergence create secret generic convergence-passthrough --from-literal=OLLAMA_CLOUD_KEY=...` (name via `--passthrough-secret`); the gateway reads each key as an env var. Keys never appear in the rendered config or manifests. `STANDIN_SALT`, `GH_TOKEN`, `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are not allowed as `api_key_env`.

Pinball's score cutoffs and per-signal points can be overridden in an optional top-level `pinball:` block of the same file (unset keys keep pinball's defaults; unknown keys are an error). Example: Pinball adds `+1` for requests with tools, which with the default `thresholds.standard: 1` puts every tool-using agent request in `standard`. To let short agent turns reach `light`:

```yaml
pinball:
  thresholds: {standard: 2, heavy: 4}
  scoring: {tools: 0}   # keys: code, heavy_keyword, max_heavy_keywords, light_keyword, long_chars, long, very_long_chars, very_long, short_chars, short, tools, effort_high, effort_low, small_output_tokens, small_output, structured
```

`convergence start` with the Ollama provider warns, with the `ollama pull` commands, when a mapped model isn't installed or Ollama can't be reached. `start --strict` turns that warning into a failure.

Responses to `auto`/`pinball` requests carry the pick as headers `x-pinball-model`, `x-pinball-tier` and `x-pinball-reason` (the body's `model` is the real upstream model).

#### How a prompt becomes a model

Scoring is done by Pinball, not Convergence; the rules live in the [Pinball README](https://github.com/jeefy/pinball#choosing-a-tier) (signal table and points, thresholds, header overrides) and the [optimizer section](https://github.com/jeefy/pinball#nightly-optimizer). In short: the score is the best-scoring user message plus request signals (code fences, heavy keywords such as `refactor` or `root cause`, length, `tools` present, reasoning effort, small `max_tokens`). Convergence renders thresholds `standard: 1` and `heavy: 3`, so a score below 1 is `light`, 3 or more is `heavy`, anything between is `standard`. Clients opt in by sending `model: auto` (or `pinball`). The gateway forwards `x-pinball-tier` (force a tier) and `x-pinball-min-tier` (raise to at least that tier) to Pinball; see the Pinball README for exact semantics. If Pinball doesn't answer, the request goes to the standard-tier model.

Worked examples, using the point values in the Pinball README (they may change with Pinball versions; `convergence doctor` shows the real decision for its probe prompts):

| Prompt (sent with `model: auto`) | Signals | Score | Tier | Pinball model | `copilot` upstream | `direct` upstream | `ollama` upstream |
|---|---|---|---|---|---|---|---|
| "Generate a title", `max_tokens: 20` | light keyword -1, short -1, small output -1 | -3 | light | `claude-haiku-5.5` | `claude-haiku-5.5` | `anthropic/claude-haiku-5-5` | `qwen2.5-coder:7b` |
| A 300 character coding question with `tools` set | tools +1 | 1 | standard | `claude-sonnet-5.5` | `claude-sonnet-5.5` | `anthropic/claude-sonnet-5-5` | `qwen2.5-coder:14b` |
| A pasted traceback and "find the root cause of this deadlock and refactor it" | code +1, two heavy keywords +2 | 3 | heavy | `claude-opus-5.5` | `claude-opus-5.5` | `anthropic/claude-opus-5-5` | `qwen2.5-coder:32b` |


## Client configuration

`convergence config <client>` edits the client's config to use the gateway. It writes a `.convergence.bak` backup first. `restore` puts the backup back; `--force` restores even if the file changed since. `--dry-run` prints the change without writing. `--gateway-url` overrides the default `http://127.0.0.1:<port>`.

| Client | Command | File changed |
|---|---|---|
| OpenCode | `convergence config opencode` | `~/.config/opencode/opencode.json` |
| oh-my-opencode | `convergence config oh-my-opencode` | `~/.omo/omo.jsonc` |
| OpenClaw | `convergence config openclaw` | `~/.openclaw/openclaw.json` |
| Claude Code | `convergence config claude-code` | `~/.claude/settings.json` |
| Aider | `convergence config aider` | `~/.aider.conf.yml` |
| Goose | `convergence config goose` | `~/.config/goose/config.yaml` |
| Hermes Agent | `convergence config hermes [--set-default]` | `config.yaml` in `$HERMES_HOME`, else `%LOCALAPPDATA%\hermes` on Windows, else `~/.hermes` |
| Continue | `convergence config continue` | `~/.continue/config.json` |
| Zed | `convergence config zed` | `~/.config/zed/settings.json` |
| Cline / Roo Code | `convergence config cline` | `~/.config/Code/User/settings.json` |
| Anything else | `convergence config copilot` | Nothing. Prints `OPENAI_*` / `ANTHROPIC_*` exports to `eval` in your shell. |

`convergence config list` shows the supported clients and whether each is currently pointed at Convergence.

## CLI reference

| Command | Description |
|---|---|
| `init [--yes] [--provider P] [--engine docker\|podman] [--compose-provider auto\|docker-compose\|podman-compose] [--sandbox S] [--copilot-token T] [--openai-key K] [--anthropic-key K] [--ollama-url U] [--ollama-model-light/-standard/-heavy M] [--bind-address A] [--port N] [--fail-open] [--image-agentgateway IMG] [--image-standin IMG] [--image-pinball IMG] [--image-copilot-api IMG] [--image-openshell IMG] [--extproc-timeout DUR]` | Write `~/.convergence/config.yaml` and render the compose stack. Interactive unless `--yes`. |
| `login copilot` | GitHub device-flow login; stores the token for the Copilot provider. |
| `start [--strict]` / `stop` / `restart` / `down` | Manage the local containers. `--strict` fails (instead of warning) when Ollama lacks a mapped model. `down` stops and removes containers and networks. |
| `prune` | Remove dangling images, stopped containers, and unused networks. |
| `status [--json]` | Per service: container state and health probe; exits non-zero if a service is down. Prints a loud warning when Stand-In is down or fail-open. |
| `doctor [--e2e] [--json] [--gateway-url URL] [--timeout D]` | Self-diagnosis; see [Doctor](#doctor). |
| `models` / `models init` | Show the tier to upstream model mapping / write a starter `~/.convergence/models.yaml`. |
| `logs [-f] [service]` | Show (or follow) logs for all services or one of `agentgateway`, `stand-in`, `pinball`, `copilot-api`. |
| `stats [--json] [--watch]` | Request counts, tokens, latency quantiles (p50/p90/p99), tier decisions and redactions read from Prometheus metrics. |
| `insights [--since 1d\|7d\|30d] [--coach] [--json] [--db-dsn DSN] [--gateway-url URL] [--reference-model M]` | Analyze prompt and response patterns, context bloat, and tier usage from agentgateway's request log with optional AI coaching. |
| `config list` | List supported clients. |
| `config <client> [--dry-run] [--gateway-url URL]` | Point a client at the gateway. |
| `config <client> restore [--force]` | Restore the client's backup. |
| `cluster install [--namespace NS] [--provider copilot\|direct] [--copilot-token T] [--openai-key K] [--anthropic-key K] [--models-file F] [--fail-open] [--ingress-host HOST] [--enable-pdb] [--request-log-db DSN \| --request-log-postgres] [--request-log-retention DUR] [--optimizer-dsn DSN] [--opt-schedule CRON] [--dry-run] [--image-agentgateway IMG] [--image-standin IMG] [--image-pinball IMG] [--image-copilot-api IMG] [--extproc-timeout DUR]` | Apply manifests to the current kube context. `--dry-run` prints them. Omitted secrets (`--copilot-token`, Stand-In salt, `--request-log-postgres-password`) are reused from the existing namespace. |
| `cluster status [--namespace NS]` | Rollout and pod health. |
| `cluster logs [-f] [--tail N] [service]` | Stream logs from in-cluster pods. |
| `cluster backup --out DIR [--namespace NS]` | `pg_dump -Fc` of the request-log Postgres plus a tar of the pinball overlay, timestamped. See [Backup and restore](#backup-and-restore). |
| `cluster restore --from FILE [--namespace NS] [--yes]` | `pg_restore --clean --if-exists` into the running Postgres (asks for confirmation). |
| `cluster uninstall [--namespace NS] [--yes]` | Delete the Convergence resources. |
| `optimize --db-dsn DSN [--dry-run]` | **Experimental.** Run a Pinball evaluation and within-tier re-ranking against a Pinball history database. |
| `sandbox list` | List supported sandboxing runtimes. |
| `sandbox status [--json]` | Show status and reachability of the configured sandboxing runtime. |
| `sandbox run [-v/--mount path] <agent> [args...]` | Run an agent inside the configured sandbox runtime with inference routed to Convergence. Can vet installed skills first (opt-in); see [Skill scanning](#skill-scanning). |
| `skills build` | Build the pinned SkillSpector scanner image. |
| `skills scan [path...] [--llm] [--model M] [--build] [--no-save]` | Scan installed agent skills (or the given paths) with SkillSpector; exits 1 when one is `DO_NOT_INSTALL`. |
| `upgrade` | Pull the image versions pinned by this binary and recreate the stack. |
| `uninstall [--purge] [--yes]` | Stop and remove the stack. `--purge` also deletes `~/.convergence`. |
| `version` | Version, commit, build date. |

`--extproc-timeout` (config: `extproc_timeout`, default `5s`) sets agentgateway's `requestTimeout` for the ext_proc policy: the time it waits for the first gRPC response headers from the ext_proc server (see agentgateway#4146). Raise it for very large requests. Must be a positive Go duration (e.g. `60s`).

`cluster install --standin-cpu-request` / `--standin-cpu-limit` (default `50m` / `500m`) set Stand-In's CPU. Stand-In is CPU-bound on large bodies; if ext_proc times out under load, raise the limit (e.g. `--standin-cpu-limit 4`) before raising `--extproc-timeout`.

## Skill scanning

Agent skills run with the agent's trust. `convergence skills scan` vets them with [NVIDIA SkillSpector](https://github.com/NVIDIA/skillspector) (prompt injection, data exfiltration, supply chain and more) in a container, so the host needs no Python. NVIDIA publishes no image: build the pinned one once.

```bash
convergence skills build      # builds localhost/convergence-skillspector:v2.13.4 from a pinned commit
convergence skills scan       # ~/.claude/skills, ~/.config/opencode/skills, ~/.agents/skills, ~/.hermes/skills
convergence skills scan ./some-skill
```

Each skill gets a score, a severity and a verdict (`SAFE`, `CAUTION`, `DO_NOT_INSTALL`). The command exits 1 when a skill is `DO_NOT_INSTALL`. The static scan runs with `--network=none` and takes seconds.

`--llm` adds SkillSpector's semantic analysis through this gateway (one to two minutes per skill). The model must honor forced tool calls, so `auto` does not work: set `skill_scan_model` in `config.yaml` or pass `--model`. If the model ignores them, the analysis is incomplete and the command says so and exits 1.

Scanning is opt-in. To have `sandbox run` check installed skills before it starts the agent, set `skill_scan` in `config.yaml`:

| `skill_scan` | Unsafe skill | Scanner missing |
|---|---|---|
| `off` (default) | no check | no check |
| `warn` | warning, the agent starts | warning, the agent starts |
| `block` | refuses to start | refuses to start |

It reuses the last static scan (`~/.convergence/skill-scan.json`) and rescans only when the installed skills changed. `doctor` reports the cached result.

## Status and doctor

`convergence status` probes each service (container state plus a health check; agentgateway is probed over its port because its image has no shell). It exits non-zero if any required service is down, and `--json` gives machine-readable output. If Stand-In is down, or the stack is fail-open, it prints a loud warning that secret scrubbing is not active (or that requests would pass unscrubbed).

<a id="doctor"></a>`convergence doctor` runs self-checks and exits 1 if any check fails:

| Check | Meaning |
|---|---|
| config | `~/.convergence/config.yaml` parses and `.env` is mode `0600` |
| engine | docker/podman answers and compose is available |
| service `<name>` | each service is running and healthy |
| port `<n>` | each published port accepts connections |
| stand-in metrics | Stand-In `/metrics` is scrapeable and in `enforce` mode |
| pinball `/check` | probe prompts get a tier and model back from Pinball |
| provider reachability | the upstream (copilot-api, Ollama, ...) answers |
| stand-in fail mode | warns if fail-open is configured or rendered |
| skills | result of the last `skills scan` of installed skills: warns when never scanned, changed since, or unsafe (fails on unsafe with `skill_scan: block`); info while `skill_scan` is off |

`--e2e` additionally sends one real request (model `auto`, `max_tokens: 16`) containing a fake GitHub token through the gateway. With a real provider this may cost a few tokens, so it's off by default.

## How stats work

`convergence stats` scrapes the Prometheus endpoints exposed by agentgateway (`:15020`), Stand-In (`:9102`) and Pinball (`:9201`) and summarizes them. Counters reset when containers restart. It shows tokens and tier choices, not money: there are no cost estimates. `--json` prints machine-readable output; `--watch` refreshes.

## Usage insights and coaching

`convergence insights` inspects agentgateway's Postgres request log across a time window (`--since 1d|7d|30d`, default `7d`) to provide usage assessment and coaching:

- **Usage and tier distributions:** Request counts, token volume (input vs output), average latency, model breakdown, and Pinball tier allocation (`light`, `standard`, `heavy`).
- **Cost and savings:** total logged cost, cost per tier and the estimated saving versus pricing the same tokens on a reference model (`--reference-model`, default the heavy-tier model; rates are pinball's optimizer estimates). Rows without a cost are excluded and counted ("N of M rows priced").
- **Pattern detection:** Identifies context bloat (massive prompts producing minimal completions), heavy-tier over-utilization on simple queries, rapid duplicate retries, and high error rates.
- **AI coaching (`--coach`):** Synthesizes actionable recommendations tailored to observed prompt patterns via the local Convergence gateway (`POST /v1/chat/completions`, model `auto`).

Requires the request-log database enabled with full LLM logging (`init --request-log-db` or cluster `--request-log-postgres` / `--request-log-db`), or passed via `--db-dsn`. `--json` outputs machine-readable data.

## Pinball optimizer

Pinball can replay sampled historical requests against candidate models and re-rank models within a tier. In the cluster, `cluster install --optimizer-dsn ...` adds a CronJob that runs weekly by default (`0 3 * * 0`, Sundays 03:00; change with `--opt-schedule`). Without `--optimizer-dsn` there's no CronJob. Locally, `convergence optimize --db-dsn ...` runs it on demand.

To make the local stack feed the optimizer, give agentgateway a Postgres you operate (reachable from the containers, e.g. `host.docker.internal`): `convergence init --request-log-db postgres://user:pass@host.docker.internal:5432/agentgateway && convergence restart`. The DSN goes to the 0600 `.env` and `config.yaml`, never into the rendered agentgateway config. `convergence optimize` then uses it without `--db-dsn`. **Privacy:** this logs every request with LLM logging `full`, so the database stores prompts and completions (after Stand-In scrubbing, which is pattern-based and not a guarantee). Protect and expire that database accordingly. It is off by default; the compose stack does not ship Postgres. In the cluster, `cluster install --request-log-postgres` renders a small Postgres (StatefulSet, 5Gi PVC, Service, NetworkPolicy allowing only agentgateway and the optimizer) with a generated password; `--request-log-db <dsn>` uses your own database instead. Either one turns on LLM logging `full` (same privacy caveat: prompts and completions are stored after scrubbing) and renders the optimizer CronJob. The DSN lives only in Secret `pinball-optimizer-db`; agentgateway and the CronJob read it from env, so it never appears in a ConfigMap or the gateway config. The CronJob uses the pinball image of the install (`--image-pinball`) and `--opt-schedule`. Re-running `cluster install` reuses the password, the Stand-In salt and the Copilot token from the Secrets of the previous install when the flags are omitted (pass the flags to rotate them), so the existing PVC keeps working; `--provider copilot` fails if there is neither `--copilot-token` nor an existing Secret. The optimizer is experimental and opt-in; replays make real model calls. See the [Pinball docs](https://github.com/jeefy/pinball).

### Backup and restore

The request log is the Pinball optimizer's training data. `convergence cluster backup --out ./bk` writes `request_logs-<ts>.dump` and `pinball-overlay-<ts>.tar` (the overlay is optional; a missing one only warns). `convergence cluster restore --from ./bk/request_logs-<ts>.dump` replaces the data in the running Postgres. `cluster install` takes a backup into `~/.convergence/backups/` first when `request-log-postgres` already exists (keeps the last 5; skip with `--no-backup`). Dumps contain prompts: keep them as protected as the database.

`cluster uninstall` deletes the namespace and, with most storage classes (reclaim policy `Delete`), the PVs. Back up first, or keep the volumes: `kubectl get pv`, then `kubectl patch pv <name> -p '{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}'` for the `request-log-postgres` and `pinball-overlay` claims. Scheduled/off-cluster backups (S3, CronJob) are not included.

**Retention.** The request log grows without bound by default. `cluster install --request-log-retention 720h` (with `--request-log-postgres` or `--request-log-db`) renders a daily CronJob `request-log-retention` (02:17) that deletes `request_logs` rows older than that duration in batches of 5000 (payloads go with them via `ON DELETE CASCADE`). Minimum `24h`, the optimizer's sampling window (`pinball-optimize --days`, default 1); shorter values fail at install. Off when omitted, and removing the flag on a re-install leaves an already-created CronJob in place until you `kubectl delete cronjob request-log-retention`. Deleted rows are gone: take a `cluster backup` first.

## Observability

Optional OTLP trace export, off by default. Convergence only configures the export; you run the collector or backend (OpenTelemetry Collector, Jaeger, Langfuse, Phoenix, Grafana Tempo, ...).

```bash
convergence init --otel-endpoint localhost:4317               # local (gRPC)
convergence init --otel-endpoint collector:4318 --otel-protocol http --otel-sample 0.25
convergence cluster install --provider copilot --otel-endpoint otel-collector.monitoring:4317
```

Or in `~/.convergence/config.yaml`:

```yaml
otel:
  endpoint: localhost:4317   # host:port; unset = no tracing
  protocol: grpc             # grpc (default) or http
  sample: 1                  # 0..1, default 1
```

Spans use `service.name=convergence` and carry agentgateway's `gen_ai.*` attributes (operation, provider, request/response model, input/output tokens) plus `caller` (from the `x-agw-caller` request header) and `pinball.tier`, `pinball.model`, `pinball.reason`, `pinball.score` for `model: auto` requests. Traces carry metadata only, never prompts or completions. Note that agentgateway's exporter honors `HTTP_PROXY`/`HTTPS_PROXY`; a proxy in its environment can break OTLP delivery. From a container, use `host.docker.internal:4317` to reach a collector on the host.

## Security model

What Stand-In does:

- Inspects request bodies on ingress and replaces detected secrets (API keys, tokens, credentials) with `<<secret-...>>` placeholders, then restores them in responses. Detection is pattern-based; tiers and rules are described in the [Stand-In README](https://github.com/jeefy/stand-in).

What it does **not** do:

- It won't catch every secret or any PII it has no rule for. Treat it as a safety net, not a guarantee.
- **Custom Stand-In patterns:** `init --standin-patterns-file ./patterns.yaml` (stored as `standin_patterns_file`) or `cluster install --standin-patterns-file ./patterns.yaml`. Compose mounts the file read-only and sets `STANDIN_PATTERNS_FILE`; the cluster install renders ConfigMap `stand-in-patterns`, mounts it and sets the env. The file **replaces** Stand-In's packaged `patterns.yaml` (no merge; Stand-In loads exactly one file), so start from a copy of the upstream file and append your rules. Unset keeps the image default.
- Stand-In is **fail-closed** by default: the gateway's ext_proc filter uses `failureMode: failClosed`, so if Stand-In is down, requests are rejected and nothing leaves unscrubbed. Opt into passthrough with `convergence init --fail-open` (local) or `convergence cluster install --fail-open` (cluster); then requests go upstream **unscrubbed** while Stand-In is down. `status` and `doctor` warn about it.
- Pinball uses `failureMode: allow` on purpose: it only picks a model tier, so if it's down the request falls back to the standard-tier model. That doesn't affect confidentiality.
- It doesn't protect the upstream response itself or anything you send through other paths.

Local exposure:

- All published ports bind to `127.0.0.1` by default (`bind_address` in `~/.convergence/config.yaml`). Change it only if you mean to expose the gateway, which has no authentication of its own.
- Provider keys and tokens are stored in plaintext in `~/.convergence/config.yaml` and `~/.convergence/.env`, both mode `0600`. Rendered compose and gateway files contain no secrets: compose reads them from the `.env` file next to it. Containers use `json-file` log rotation (10 MB, 3 files); Pinball and copilot-api have healthchecks, while agentgateway and Stand-In are distroless images probed over their ports by `convergence status`. In the cluster, keys live in Kubernetes Secrets.

Limitations:

- The gateway has no authentication of its own. Any local process that can reach `127.0.0.1:3000` can use your provider keys through it. In Kubernetes, any pod the NetworkPolicy allows can do the same.
- Fail-closed means a Stand-In outage rejects requests until it's back.
- Detection is pattern-based; see above.

Report vulnerabilities privately: see [SECURITY.md](SECURITY.md).

## Troubleshooting

**Port already in use.** Another process has `3000` (or `4141`, `9102`, `9201`, `15020`). Find it with `ss -ltnp | grep 3000` (Linux) or `lsof -i :3000` (macOS). Re-run `convergence init --port 3100` for the gateway; the other ports are fixed.

**Podman rootless errors.** Check that `podman info` works first. If using Docker Compose or OpenShell, enable Podman's user API socket (`systemctl --user enable --now podman.socket`). `podman-compose` itself does not require that socket; select it with `convergence init --engine podman --compose-provider podman-compose`. SELinux denials on mounted config files are handled with the shared `z` volume label on Linux; if you edited the compose file by hand, re-run `convergence init`.

**Podman reports `nft` JSON support is not compiled in.** Netavark needs an `nft` binary with JSON support. On Linux, a Homebrew `nftables` installation may take precedence over the system binary without providing that support. Check `command -v nft`; if it points to Homebrew and a suitable system `nft` is installed, run `brew unlink nftables`, verify that `command -v nft` selects the system binary, and retry `convergence start`. Unlinking leaves the Homebrew package installed; `brew link nftables` restores its links. Unlinking can also affect other applications that use Homebrew's `nft`.

**OpenShell cannot connect to the Podman API socket.** Check `podman info --format '{{.Host.RemoteSocket.Path}}'` and enable the local Podman socket. Convergence checks and mounts that path when starting OpenShell; a remote socket path must also be accessible to the local Compose engine.

**Podman label-template errors.** If `start` reports `can't evaluate field Label`, upgrade Convergence. Running-service checks now use Podman's `Labels` map, which also works on older distro releases.

**Podman exits with status 125 during startup.** Running-service checks use native `podman ps`, compatible with either Compose provider. Check the stderr included in the error and try `podman ps` directly to diagnose storage or permission failures.

**Health check failing.** Run `convergence status`, then `convergence logs <service>`. Common causes: an image pull failed (check network/registry access), `STANDIN_SALT missing` (re-run `convergence init`), or a bad provider key (gateway logs show 401s from upstream).

**Copilot auth.** `401`/`403` from `copilot-api` means the token is missing, expired, or the account has no Copilot subscription. Run `convergence login copilot` again, then `convergence restart`. `convergence logs copilot-api` shows the auth result.

## Versioning and upgrades

Convergence follows [Semantic Versioning](https://semver.org/). Before 1.0, minor releases may include breaking changes; they're called out in [CHANGELOG.md](CHANGELOG.md). Each release pins exact image tags for agentgateway, Stand-In, Pinball and copilot-api. To upgrade, install the new binary (re-run the installer) and then run `convergence upgrade`. Image tags aren't digest-pinned yet.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
