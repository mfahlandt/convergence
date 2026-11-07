# Convergence

Convergence runs an AI gateway stack on your workstation or in a Kubernetes cluster and points your coding agents at it. It wires together several projects:

- [agentgateway](https://agentgateway.dev/docs/) (`v1.6.0`): the gateway. It serves an OpenAI-compatible `/v1` endpoint (Anthropic Messages requests are translated) and forwards to your provider.
- [Stand-In](https://github.com/jeefy/stand-in) (`0.6.0`): an `ext_proc` filter that swaps secrets and credentials in prompts for opaque tokens before they leave your machine, then puts the originals back in responses.
- [Pinball](https://github.com/jeefy/pinball) (`0.7.4`): an `ext_authz` service that scores each sanitized request and picks a model tier (`light`, `standard`, `heavy`).
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

- Linux or macOS on amd64 or arm64. Windows isn't supported; WSL2 works.
- For the local stack: Docker with the compose plugin, or Podman with `podman-compose` (or `podman compose`).
- For the cluster mode: `kubectl` access to a cluster.
- Credentials for one provider (see [Providers](#providers)).

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

With an isolated sandbox:

```bash
convergence init --yes --provider direct --sandbox nono
convergence start
convergence sandbox run opencode
```

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

Pinball's score cutoffs and per-signal points can be overridden in an optional top-level `pinball:` block of the same file (unset keys keep pinball's defaults; unknown keys are an error). Example: Pinball adds `+1` for requests with tools, which with the default `thresholds.standard: 1` puts every tool-using agent request in `standard`. To let short agent turns reach `light`:

```yaml
pinball:
  thresholds: {standard: 2, heavy: 4}
  scoring: {tools: 0}   # keys: code, heavy_keyword, max_heavy_keywords, light_keyword, long_chars, long, very_long_chars, very_long, short_chars, short, tools, effort_high, effort_low, small_output_tokens, small_output, structured
```

`convergence start` with the Ollama provider warns, with the `ollama pull` commands, when a mapped model isn't installed or Ollama can't be reached. `start --strict` turns that warning into a failure.

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
| Claude Code | `convergence config claude-code` | `~/.claude/settings.json` |
| Aider | `convergence config aider` | `~/.aider.conf.yml` |
| Goose | `convergence config goose` | `~/.config/goose/config.yaml` |
| Hermes Agent | `convergence config hermes` | `~/.hermes/config.yaml` |
| Continue | `convergence config continue` | `~/.continue/config.json` |
| Zed | `convergence config zed` | `~/.config/zed/settings.json` |
| Cline / Roo Code | `convergence config cline` | `~/.config/Code/User/settings.json` |
| Anything else | `convergence config copilot` | Nothing. Prints `OPENAI_*` / `ANTHROPIC_*` exports to `eval` in your shell. |

`convergence config list` shows the supported clients and whether each is currently pointed at Convergence.

## CLI reference

| Command | Description |
|---|---|
| `init [--yes] [--provider P] [--engine docker\|podman] [--sandbox S] [--copilot-token T] [--openai-key K] [--anthropic-key K] [--ollama-url U] [--ollama-model-light/-standard/-heavy M] [--bind-address A] [--port N] [--fail-open] [--image-agentgateway IMG] [--image-standin IMG] [--image-pinball IMG] [--image-copilot-api IMG] [--image-openshell IMG]` | Write `~/.convergence/config.yaml` and render the compose stack. Interactive unless `--yes`. |
| `login copilot` | GitHub device-flow login; stores the token for the Copilot provider. |
| `start [--strict]` / `stop` / `restart` / `down` | Manage the local containers. `--strict` fails (instead of warning) when Ollama lacks a mapped model. `down` stops and removes containers and networks. |
| `prune` | Remove dangling images, stopped containers, and unused networks. |
| `status [--json]` | Per service: container state and health probe; exits non-zero if a service is down. Prints a loud warning when Stand-In is down or fail-open. |
| `doctor [--e2e] [--json] [--gateway-url URL] [--timeout D]` | Self-diagnosis; see [Doctor](#doctor). |
| `models` / `models init` | Show the tier to upstream model mapping / write a starter `~/.convergence/models.yaml`. |
| `logs [-f] [service]` | Show (or follow) logs for all services or one of `agentgateway`, `stand-in`, `pinball`, `copilot-api`. |
| `stats [--json] [--watch]` | Request counts, tokens, latency quantiles (p50/p90/p99), tier decisions and redactions read from Prometheus metrics. |
| `config list` | List supported clients. |
| `config <client> [--dry-run] [--gateway-url URL]` | Point a client at the gateway. |
| `config <client> restore [--force]` | Restore the client's backup. |
| `cluster install [--namespace NS] [--provider copilot\|direct] [--copilot-token T] [--openai-key K] [--anthropic-key K] [--models-file F] [--fail-open] [--ingress-host HOST] [--enable-pdb] [--optimizer-dsn DSN] [--opt-schedule CRON] [--dry-run] [--image-agentgateway IMG] [--image-standin IMG] [--image-pinball IMG] [--image-copilot-api IMG]` | Apply manifests to the current kube context. `--dry-run` prints them. |
| `cluster status [--namespace NS]` | Rollout and pod health. |
| `cluster logs [-f] [--tail N] [service]` | Stream logs from in-cluster pods. |
| `cluster uninstall [--namespace NS] [--yes]` | Delete the Convergence resources. |
| `optimize --db-dsn DSN [--dry-run]` | **Experimental.** Run a Pinball evaluation and within-tier re-ranking against a Pinball history database. |
| `sandbox list` | List supported sandboxing runtimes. |
| `sandbox status [--json]` | Show status and reachability of the configured sandboxing runtime. |
| `sandbox run [-v/--mount path] <agent> [args...]` | Run an agent inside the configured sandbox runtime with inference routed to Convergence. |
| `upgrade` | Pull the image versions pinned by this binary and recreate the stack. |
| `uninstall [--purge] [--yes]` | Stop and remove the stack. `--purge` also deletes `~/.convergence`. |
| `version` | Version, commit, build date. |

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

`--e2e` additionally sends one real request (model `auto`, `max_tokens: 16`) containing a fake GitHub token through the gateway. With a real provider this may cost a few tokens, so it's off by default.

## How stats work

`convergence stats` scrapes the Prometheus endpoints exposed by agentgateway (`:15020`), Stand-In (`:9102`) and Pinball (`:9201`) and summarizes them. Counters reset when containers restart. It shows tokens and tier choices, not money: there are no cost estimates. `--json` prints machine-readable output; `--watch` refreshes.

## Pinball optimizer

Pinball can replay sampled historical requests against candidate models and re-rank models within a tier. In the cluster, `cluster install --optimizer-dsn ...` adds a CronJob that runs weekly by default (`0 3 * * 0`, Sundays 03:00; change with `--opt-schedule`). Without `--optimizer-dsn` there's no CronJob. Locally, `convergence optimize --db-dsn ...` runs it on demand. The optimizer is experimental and opt-in; replays make real model calls. See the [Pinball docs](https://github.com/jeefy/pinball).

## Security model

What Stand-In does:

- Inspects request bodies on ingress and replaces detected secrets (API keys, tokens, credentials) with `<<secret-...>>` placeholders, then restores them in responses. Detection is pattern-based; tiers and rules are described in the [Stand-In README](https://github.com/jeefy/stand-in).

What it does **not** do:

- It won't catch every secret or any PII it has no rule for. Treat it as a safety net, not a guarantee.
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

**Podman rootless permission errors.** Make sure the user socket is running (`systemctl --user enable --now podman.socket`) and that `podman-compose` or `podman compose` works on its own. SELinux denials on mounted config files are handled with the shared `z` volume label on Linux; if you edited the compose file by hand, re-run `convergence init`.

**Health check failing.** Run `convergence status`, then `convergence logs <service>`. Common causes: an image pull failed (check network/registry access), `STANDIN_SALT missing` (re-run `convergence init`), or a bad provider key (gateway logs show 401s from upstream).

**Copilot auth.** `401`/`403` from `copilot-api` means the token is missing, expired, or the account has no Copilot subscription. Run `convergence login copilot` again, then `convergence restart`. `convergence logs copilot-api` shows the auth result.

## Versioning and upgrades

Convergence follows [Semantic Versioning](https://semver.org/). Before 1.0, minor releases may include breaking changes; they're called out in [CHANGELOG.md](CHANGELOG.md). Each release pins exact image tags for agentgateway, Stand-In, Pinball and copilot-api. To upgrade, install the new binary (re-run the installer) and then run `convergence upgrade`. Image tags aren't digest-pinned yet.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
