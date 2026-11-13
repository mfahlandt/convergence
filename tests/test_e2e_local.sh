#!/usr/bin/env bash
# tests/test_e2e_local.sh - E2E: real requests through AgentGateway -> Stand-In -> Pinball -> stub upstream.
# Runs once per available engine (docker, podman). Override with ENGINES="docker".
# COMPOSE_PROVIDER selects auto, docker-compose or podman-compose for Podman.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN="${ROOT_DIR}/bin/convergence"
STUB_PY="${SCRIPT_DIR}/stub-upstream/stub.py"
STUB_IMAGE="${STUB_IMAGE:-docker.io/library/python:3.12-alpine}"
AGW_IMAGE="cr.agentgateway.dev/agentgateway:v1.6.0"
SKILLSPECTOR_IMAGE="localhost/convergence-skillspector:v2.13.4"   # DefaultImage in internal/skillscan
SKILLSPECTOR_COMMIT="54824463814a0be01360792523ec7245a69190fc"    # SourceCommit in internal/skillscan
GW=3000; STANDIN=9102; PINBALL=9201; STATS=15020   # fixed by the compose template

[ -x "$BIN" ] || { echo "Building convergence binary..."; (cd "$ROOT_DIR" && make build); }

ORIG_HOME="$HOME"
# keep container storage/config at the real location while HOME is sandboxed
export XDG_DATA_HOME="${XDG_DATA_HOME:-$ORIG_HOME/.local/share}"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$ORIG_HOME/.config}"
TEST_HOME=""
STUB_NAME="convergence-e2e-stub-$$"
# own compose project, so down/uninstall here can never touch a real "convergence" stack
export COMPOSE_PROJECT_NAME="cvg-e2e-$$"
CUR_ENGINE=""
PODMAN_SVC_PID=""
COMPOSE_PROVIDER="${COMPOSE_PROVIDER:-auto}"

fail() { echo "ERROR: $*" >&2; exit 1; }

dump_diagnostics() { # best-effort container state and logs, so CI failures are diagnosable
  [ -n "$CUR_ENGINE" ] || return 0
  echo "==> DIAGNOSTICS ($CUR_ENGINE)" >&2
  "$CUR_ENGINE" ps -a --format '{{.Names}}\t{{.Status}}' 2>&1 | head -20 >&2 || true
  local c
  for c in $("$CUR_ENGINE" ps -a --format '{{.Names}}' 2>/dev/null | grep -E "^(convergence|${COMPOSE_PROJECT_NAME})[-_]" || true); do
    echo "--- $c health:" >&2
    "$CUR_ENGINE" inspect --format '{{json .State.Health}}' "$c" 2>&1 | cut -c1-1500 >&2 || true
    echo "--- $c logs (tail):" >&2
    "$CUR_ENGINE" logs --tail 25 "$c" 2>&1 | cut -c1-300 >&2 || true
  done
}

cleanup() {
  local rc=$?
  [ "$rc" -eq 0 ] || dump_diagnostics
  if [ -n "$TEST_HOME" ]; then
    HOME="$TEST_HOME" "$BIN" stop >/dev/null 2>&1 || true
  fi
  [ -n "$PODMAN_SVC_PID" ] && kill "$PODMAN_SVC_PID" >/dev/null 2>&1 || true
  [ -n "$CUR_ENGINE" ] && "$CUR_ENGINE" rm -f "$STUB_NAME" >/dev/null 2>&1 || true
  [ -z "$TEST_HOME" ] || rm -rf "$TEST_HOME"
  return 0
}
trap cleanup EXIT

# bin_has <pattern> <convergence args...>: capture output first, then grep. `cmd | grep -q` SIGPIPEs
# cmd once grep exits, which pipefail reports as a failure (podman's compose banner trips it).
bin_has() {
  local pat="$1" o; shift
  o="$("$BIN" "$@" 2>&1)" || return 1
  grep -qiF -- "$pat" <<<"$o"
}

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("",0));print(s.getsockname()[1])'; }

metric() { # metric <port> <name> -> sum of all non-bucket series
  curl -sf "http://127.0.0.1:$1/metrics" | awk -v n="$2" '$0 !~ /^#/ { split($1,a,"{"); if (a[1]==n) s+=$NF } END{printf "%d", s+0}'
}

# out_of <convergence args...>: combined output regardless of exit code, for asserting on failures
out_of() { "$BIN" "$@" 2>&1 || true; }

fetch_skillspector_fixtures() { # copy SkillSpector's own malicious_skill and safe_skill fixtures into $1
  local d; d="$(mktemp -d)"
  git -C "$d" init -q
  git -C "$d" fetch -q --depth 1 https://github.com/NVIDIA/skillspector.git "$SKILLSPECTOR_COMMIT"
  git -C "$d" checkout -q FETCH_HEAD -- tests/fixtures/malicious_skill tests/fixtures/safe_skill
  cp -r "$d/tests/fixtures/malicious_skill" "$d/tests/fixtures/safe_skill" "$1/"
  rm -rf "$d"
}

wait_for() { # wait_for <desc> <tries> <cmd...>
  local desc="$1" tries="$2"; shift 2
  for _ in $(seq "$tries"); do "$@" >/dev/null 2>&1 && return 0; sleep 2; done
  fail "timed out waiting for $desc"
}

run_suite() {
  local engine="$1"
  CUR_ENGINE="$engine"
  echo; echo "################ engine: $engine ################"
  TEST_HOME="$(mktemp -d -t convergence-e2e-XXXXXX)"
  export HOME="$TEST_HOME"
  local cfgdir="$TEST_HOME/.convergence"
  local compose_args=()
  local original_docker_host="${DOCKER_HOST-}" docker_host_set="${DOCKER_HOST+x}"
  local unavailable_socket="$TEST_HOME/unavailable-podman.sock"
  if [ "$engine" = podman ]; then
    compose_args=(--compose-provider "$COMPOSE_PROVIDER")
    if [ "$COMPOSE_PROVIDER" = podman-compose ]; then
      echo "==> Testing podman-compose with an unavailable API socket"
      [ ! -e "$unavailable_socket" ] || fail "socketless test endpoint already exists"
      export DOCKER_HOST="unix://$unavailable_socket"
    fi
  fi

  if [ "$engine" = podman ] && [ "$COMPOSE_PROVIDER" != podman-compose ] && [ ! -S "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock" ]; then
    echo "==> Starting temporary podman API socket (compose provider needs it)"
    mkdir -p "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman"
    podman system service --time=0 >/dev/null 2>&1 &
    PODMAN_SVC_PID=$!
    sleep 2
  fi

  echo "==> Starting stub upstream"
  local sport; sport="$(free_port)"
  "$engine" rm -f "$STUB_NAME" >/dev/null 2>&1 || true
  "$engine" run -d --name "$STUB_NAME" -p "$sport:8080" -v "$STUB_PY:/stub.py:ro,Z" \
    "$STUB_IMAGE" python -u /stub.py >/dev/null
  wait_for "stub upstream" 20 curl -sf "http://127.0.0.1:$sport/v1/models"

  echo "==> convergence init (provider ollama -> stub on host port $sport)"
  if "$BIN" init --help 2>&1 | grep -q -- '--yes'; then
    "$BIN" init --yes --provider ollama --ollama-url "http://host.docker.internal:$sport" --engine "$engine" "${compose_args[@]}"
  else
    printf '3\nhttp://host.docker.internal:%s\n' "$sport" | "$BIN" init
  fi
  # force the engine under test (older init detects docker first)
  if grep -q '^engine:' "$cfgdir/config.yaml"; then
    sed -i "s/^engine:.*/engine: $engine/" "$cfgdir/config.yaml"
  fi
  if [ "$engine" = podman ]; then
    grep -q "^compose_provider: $COMPOSE_PROVIDER$" "$cfgdir/config.yaml" || fail "compose provider not persisted"
  fi
  chmod 755 "$cfgdir"; chmod 644 "$cfgdir"/*.yaml

  echo "==> Validating rendered AgentGateway config"
  local envf=(); [ -f "$cfgdir/.env" ] && envf=(--env-file "$cfgdir/.env")
  "$engine" run --rm "${envf[@]}" -v "$cfgdir":/c:Z "$AGW_IMAGE" -f /c/agentgateway.yaml --validate-only

  echo "==> convergence start"
  "$BIN" start
  wait_for "gateway /v1/models" 30 curl -sf "http://127.0.0.1:$GW/v1/models"
  wait_for "stand-in metrics" 30 curl -sf "http://127.0.0.1:$STANDIN/metrics"
  wait_for "pinball metrics" 30 curl -sf "http://127.0.0.1:$PINBALL/metrics"
  "$BIN" status
  if [ "$engine" = podman ] && [ "$COMPOSE_PROVIDER" = podman-compose ]; then
    [ ! -e "$unavailable_socket" ] || fail "podman-compose unexpectedly created an API socket"
  fi

  echo "==> Testing sandbox status & list"
  bin_has "none" sandbox list || fail "sandbox list missing none"
  bin_has "none" sandbox status || fail "sandbox status missing none"
  bin_has '"type": "none"' sandbox status --json || fail "sandbox status not none"

  echo "==> Pinball /check directly"
  local pr
  pr="$(curl -sf -X POST "http://127.0.0.1:$PINBALL/check" -H 'Content-Type: application/json' \
    -d '{"model":"auto","messages":[{"role":"user","content":"How do I debug this distributed security race condition?"}]}')"
  echo "$pr" | grep -q model || fail "unexpected pinball response: $pr"

  echo "==> Baseline stats (before real traffic)"
  local base_matches base_restored base_dec
  base_matches="$(metric $STANDIN standin_matches_total)"
  base_restored="$(metric $STANDIN standin_restored_total)"
  base_dec="$(metric $PINBALL pinball_decisions_total)"

  local token
  # python, not tr|head: head closing the pipe SIGPIPEs tr, which pipefail turns into a failure
  token="ghp_$(python3 -c 'import secrets,string; print("".join(secrets.choice(string.ascii_letters+string.digits) for _ in range(36)))')"
  local body="{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"please store my token $token now\"}]}"

  echo "==> Non-streaming round trip"
  local resp
  resp="$(curl -sf -m 30 "http://127.0.0.1:$GW/v1/chat/completions" -H 'Content-Type: application/json' -d "$body")"
  echo "$resp" | grep -qF "$token" || fail "client response lacks the ORIGINAL token (not restored): $resp"

  echo "==> Streaming (SSE) round trip"
  local sse
  sse="$(curl -sfN -m 30 "http://127.0.0.1:$GW/v1/chat/completions" -H 'Content-Type: application/json' \
    -d "${body/\"model\":\"auto\"/\"model\":\"auto\",\"stream\":true}")"
  echo "$sse" | grep -q '^data: ' || fail "no SSE frames: $sse"
  # the token may be split across frames; reassemble delta content
  local joined
  joined="$(echo "$sse" | sed -n 's/^data: //p' | python3 -c '
import sys, json
out = ""
for l in sys.stdin:
    l = l.strip()
    if not l or l == "[DONE]": continue
    for c in json.loads(l).get("choices", []):
        out += c.get("delta", {}).get("content") or ""
print(out)')"
  echo "$joined" | grep -qF "$token" || fail "streamed content lacks ORIGINAL token: $joined"

  echo "==> Asserting what the upstream actually received"
  local received; received="$(curl -sf "http://127.0.0.1:$sport/_received")"
  echo "$received" | grep -qF "$token" && fail "RAW token leaked to upstream"
  [ "$(echo "$received" | grep -c '<<secret-')" -ge 2 ] || fail "upstream did not receive placeholders in both requests: $received"
  echo "$received" | grep -q '"stream":true' || fail "upstream never saw the stream request"

  echo "==> Asserting metrics"
  sleep 1
  local m r d tin
  m="$(metric $STANDIN standin_matches_total)"; r="$(metric $STANDIN standin_restored_total)"
  d="$(metric $PINBALL pinball_decisions_total)"
  [ "$m" -ge $((base_matches + 2)) ] || fail "standin_matches_total $base_matches -> $m"
  [ "$r" -ge $((base_restored + 2)) ] || fail "standin_restored_total $base_restored -> $r"
  [ "$d" -ge $((base_dec + 2)) ] || fail "pinball_decisions_total $base_dec -> $d"
  tin="$(metric $STATS agentgateway_gen_ai_client_token_usage_sum)"
  [ "$tin" -gt 0 ] || fail "agentgateway token usage metric is zero"

  echo "==> convergence stats"
  local out; out="$("$BIN" stats)"; echo "$out"
  echo "$out" | grep -qE 'Credentials tokenized: +[1-9]' || fail "stats: tokenized is zero"
  echo "$out" | grep -qE 'Credentials restored: +[1-9]' || fail "stats: restored is zero"
  echo "$out" | grep -qE 'Decisions: [1-9]' || fail "stats: pinball decisions is zero"
  echo "$out" | grep -qE 'Requests: +[1-9]' || fail "stats: gateway requests is zero"
  "$BIN" stats --json | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["standin"]["credentials_restored"]>0 and d["pinball"]["decisions"]>0'

  echo "==> convergence insights without request log database warns with guidance"
  local in_err
  in_err="$("$BIN" insights 2>&1)" && fail "insights must exit non-zero when no request log db is configured"
  echo "$in_err" | grep -q "requires agentgateway's Postgres request log" || fail "insights missing DB guidance: $in_err"

  echo "==> Read-only subcommands: version, models, logs, completion, doctor"
  bin_has "v" version || fail "version printed nothing"
  bin_has "light" models || fail "models output lacks the light tier"
  # capture first: `cmd | grep -q` SIGPIPEs cmd once grep exits, which pipefail reports as failure
  local lg
  lg="$("$BIN" logs pinball 2>&1)" || fail "logs pinball failed"
  [ -n "$lg" ] || fail "logs pinball printed nothing"
  lg="$("$BIN" logs agentgateway 2>&1)" || fail "logs agentgateway failed"
  [ -n "$lg" ] || fail "logs agentgateway printed nothing"
  "$BIN" logs bogus-service >/dev/null 2>&1 && fail "logs must reject unknown services"
  bin_has "convergence" completion bash || fail "completion bash printed nothing"
  "$BIN" doctor || fail "doctor failed on a healthy stack"

  echo "==> config <client>: dry-run, configure, idempotent, restore"
  local cc="$TEST_HOME/.claude/settings.json"
  bin_has 'dry-run' config claude-code --dry-run || fail "config dry-run output"
  [ ! -e "$cc" ] || fail "config --dry-run wrote a file"
  "$BIN" config claude-code >/dev/null
  grep -q "127.0.0.1\|localhost" "$cc" || fail "claude-code settings not pointed at the gateway"
  bin_has 'already up to date' config claude-code || fail "config not idempotent"
  bin_has "claude-code" config list || fail "config list missing claude-code"
  "$BIN" config claude-code restore >/dev/null
  if [ -e "$cc" ]; then grep -q "localhost\|127.0.0.1" "$cc" && fail "restore left the Convergence keys"; fi

  echo "==> skills: scan needs the image, build it, scan upstream fixtures"
  local skills="$TEST_HOME/.claude/skills" scan_out
  mkdir -p "$skills"
  fetch_skillspector_fixtures "$skills"
  "$engine" image rm -f "$SKILLSPECTOR_IMAGE" >/dev/null 2>&1 || true
  scan_out="$(out_of skills scan)"
  echo "$scan_out" | grep -q "skills build" || fail "skills scan without image lacks the build hint: $scan_out"
  "$BIN" skills build >/dev/null
  "$engine" image inspect "$SKILLSPECTOR_IMAGE" >/dev/null || fail "skills build left no image"
  scan_out="$("$BIN" skills scan 2>&1)" && fail "skills scan must exit 1 with a DO_NOT_INSTALL skill"
  echo "$scan_out"
  echo "$scan_out" | grep -q "DO_NOT_INSTALL" || fail "malicious fixture not flagged"
  echo "$scan_out" | grep -qE "safe-greeting.*SAFE" || fail "safe fixture not SAFE"
  echo "$scan_out" | grep -q "WARNING: analysis incomplete" && fail "static scan reported incomplete"
  [ "$(stat -c %a "$cfgdir/skill-scan.json")" = 600 ] || fail "skill-scan.json is not 0600"
  python3 -c 'import sys,json; d=json.load(open(sys.argv[1])); assert d["complete"] and len(d["skills"])==2, d' "$cfgdir/skill-scan.json"
  "$BIN" skills scan "$skills/safe_skill" >/dev/null || fail "scanning only the safe fixture must exit 0"

  echo "==> skills: sandbox run and doctor honor skill_scan warn/block from the cached scan"
  sed -i '/^skill_scan:/d;/^sandbox:/d' "$cfgdir/config.yaml"
  printf 'sandbox: nono\nskill_scan: block\n' >> "$cfgdir/config.yaml"
  scan_out="$("$BIN" sandbox run true 2>&1)" && fail "sandbox run must refuse with skill_scan: block"
  echo "$scan_out" | grep -q "DO_NOT_INSTALL" || fail "sandbox run block message lacks the verdict: $scan_out"
  "$BIN" doctor >/dev/null 2>&1 && fail "doctor must fail on an unsafe skill with skill_scan: block"
  out_of doctor | grep -q "FAIL skills" || fail "doctor missing FAIL skills"
  sed -i 's/^skill_scan:.*/skill_scan: warn/' "$cfgdir/config.yaml"
  out_of doctor | grep -q "WARN skills" || fail "doctor missing WARN skills in warn mode"
  rm -rf "$skills/malicious_skill"
  out_of doctor | grep -q "changed since" || fail "doctor did not notice the skills changed"
  scan_out="$(out_of sandbox run true)"
  echo "$scan_out" | grep -q "Installed skills changed; scanning" || fail "sandbox run did not rescan changed skills: $scan_out"
  echo "$scan_out" | grep -q "DO_NOT_INSTALL" && fail "rescan still flags a removed skill: $scan_out"
  out_of doctor | grep -q "PASS skills" || fail "doctor missing PASS skills after rescan"
  sed -i 's/^skill_scan:.*/skill_scan: bogus/' "$cfgdir/config.yaml"
  out_of doctor | grep -q "invalid skill_scan" || fail "invalid skill_scan not rejected"
  sed -i '/^skill_scan:/d;/^sandbox:/d' "$cfgdir/config.yaml"
  rm -rf "$skills"

  echo "==> restart keeps the stack serving"
  "$BIN" restart
  wait_for "gateway after restart" 30 curl -sf "http://127.0.0.1:$GW/v1/models"
  wait_for "stand-in after restart" 30 curl -sf "http://127.0.0.1:$STANDIN/metrics"

  echo "==> upgrade (re-render, pull pinned images, recreate)"
  "$BIN" upgrade
  wait_for "gateway after upgrade" 30 curl -sf "http://127.0.0.1:$GW/v1/models"
  "$BIN" status

  echo "==> fail-closed default: requests are rejected while Stand-In is down"
  local standin_c
  standin_c="$("$engine" ps --format '{{.Names}}' | grep -E "^${COMPOSE_PROJECT_NAME}[-_]stand-in" | head -1)"
  [ -n "$standin_c" ] || fail "stand-in container not found"
  "$engine" stop "$standin_c" >/dev/null
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 30 "http://127.0.0.1:$GW/v1/chat/completions" \
    -H 'Content-Type: application/json' -d "$body")"
  case "$code" in 2*) fail "fail-closed: request succeeded ($code) with Stand-In down";; esac
  echo "    rejected with HTTP $code"

  echo "==> --fail-open + OTel flags re-render and the stack serves again"
  "$BIN" init --yes --provider ollama --ollama-url "http://host.docker.internal:$sport" --engine "$engine" \
    --fail-open --otel-endpoint 127.0.0.1:4317 --otel-sample 0.5 >/dev/null
  if [ "$engine" = podman ]; then
    grep -q "^compose_provider: $COMPOSE_PROVIDER$" "$cfgdir/config.yaml" || fail "re-init lost compose provider"
  fi
  if grep -q '^engine:' "$cfgdir/config.yaml"; then sed -i "s/^engine:.*/engine: $engine/" "$cfgdir/config.yaml"; fi
  chmod 755 "$cfgdir"; chmod 644 "$cfgdir"/*.yaml
  grep -q 'failureMode: failOpen' "$cfgdir/agentgateway.yaml" || fail "failOpen not rendered"
  grep -q 'tracing:' "$cfgdir/agentgateway.yaml" || fail "otel tracing not rendered"
  "$BIN" restart
  wait_for "gateway after fail-open restart" 30 curl -sf "http://127.0.0.1:$GW/v1/models"
  standin_c="$("$engine" ps --format '{{.Names}}' | grep -E "^${COMPOSE_PROJECT_NAME}[-_]stand-in" | head -1)"
  "$engine" stop "$standin_c" >/dev/null
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 30 "http://127.0.0.1:$GW/v1/chat/completions" \
    -H 'Content-Type: application/json' -d "$body")"
  case "$code" in 2*) ;; *) fail "fail-open: request not served ($code) with Stand-In down";; esac

  echo "==> Stopping stack"
  "$BIN" stop
  "$BIN" stats >/dev/null 2>&1 && fail "stats should exit non-zero when every source is down"

  echo "==> down, then uninstall --purge --yes"
  if ! "$BIN" down; then
    [ "$engine" = podman ] || fail "down failed"
    echo "WARN: podman down failed, retrying"
    sleep 2
    "$BIN" down || "$engine" ps -a --format '{{.Names}}' \
      | grep -E "^${COMPOSE_PROJECT_NAME}[-_]" | xargs -r "$engine" rm -f >/dev/null 2>&1 || true
  fi
  [ -z "$("$engine" ps -a --format '{{.Names}}' | grep -E "^${COMPOSE_PROJECT_NAME}[-_](agentgateway|stand-in|pinball)" || true)" ] || fail "stack containers remain after down"
  "$BIN" uninstall --purge --yes
  [ ! -e "$cfgdir" ] || fail "uninstall --purge left $cfgdir"
  "$engine" rm -f "$STUB_NAME" >/dev/null 2>&1 || true
  rm -rf "$TEST_HOME"; TEST_HOME=""
  export HOME="$ORIG_HOME"
  if [ "$docker_host_set" = x ]; then
    export DOCKER_HOST="$original_docker_host"
  else
    unset DOCKER_HOST
  fi
  echo "==> engine $engine PASSED"
}

ENGINES="${ENGINES:-}"
if [ -z "$ENGINES" ]; then
  for e in docker podman; do command -v "$e" >/dev/null 2>&1 && ENGINES="$ENGINES $e"; done
fi
[ -n "$ENGINES" ] || fail "neither docker nor podman found"
for e in $ENGINES; do run_suite "$e"; done
echo; echo "==> Local E2E test PASSED (engines:$ENGINES)"
