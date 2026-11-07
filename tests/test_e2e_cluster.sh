#!/usr/bin/env bash
# tests/test_e2e_cluster.sh - Cluster Manifests and Live KinD E2E Test
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN="${ROOT_DIR}/bin/convergence"
NAMESPACE="convergence-e2e"
STUB_NS="${NAMESPACE}-stub"

if [ ! -x "$BIN" ]; then
  (cd "$ROOT_DIR" && make build)
fi

echo "==> Testing 'convergence cluster install --dry-run'..."
OUTPUT="$("$BIN" cluster install --dry-run --namespace="${NAMESPACE}")"

# Verify key resources exist in rendered YAML
for resource in "Namespace" "Secret" "ConfigMap" "Deployment" "Service"; do
  if ! echo "$OUTPUT" | grep -q "kind: $resource"; then
    echo "ERROR: Rendered manifests missing resource kind: $resource"
    exit 1
  fi
done

if command -v kubectl >/dev/null 2>&1; then
  echo "==> Validating manifests against kubectl client schema..."
  echo "$OUTPUT" | kubectl apply --dry-run=client -f -
fi

# Check if a live cluster is reachable
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
  echo "==> Live Kubernetes cluster detected! Running in-cluster E2E test..."

  cleanup_cluster() {
    echo "==> Cleaning up namespace ${NAMESPACE}..."
    kubectl delete namespace "${NAMESPACE}" "${STUB_NS}" --ignore-not-found=true || true
  }
  trap cleanup_cluster EXIT

  PF_PIDS=()
  free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
  STUB_PORT="$(free_port)"
  dump_debug() {
    echo "==> FAILURE: dumping cluster state" >&2
    kubectl -n "${NAMESPACE}" get pods -o wide || true
    kubectl -n "${NAMESPACE}" describe pods || true
    for p in $(kubectl -n "${NAMESPACE}" get pods -o name 2>/dev/null); do
      echo "--- logs ${p}" >&2
      kubectl -n "${NAMESPACE}" logs "${p}" --all-containers --tail=100 || true
    done
  }
  cleanup_all() {
    for pid in "${PF_PIDS[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; done
    cleanup_cluster
  }
  on_err() { dump_debug; }
  trap on_err ERR
  trap cleanup_all EXIT

  echo "==> Deploying stub OpenAI upstream into ${STUB_NS}..."
  kubectl create namespace "${STUB_NS}"
  kubectl -n "${STUB_NS}" create configmap stub-upstream --from-file=stub.py="${SCRIPT_DIR}/stub-upstream/stub.py"
  kubectl -n "${STUB_NS}" apply -f "${SCRIPT_DIR}/stub-upstream/stub-k8s.yaml"
  kubectl -n "${STUB_NS}" rollout status deployment/stub-upstream --timeout=300s

  # Route every tier to the (OpenAI-compatible) stub instead of real Anthropic.
  MODELS_FILE="$(mktemp)"
  cat >"${MODELS_FILE}" <<'EOF'
direct:
  claude-haiku-5.5: "openai:stub"
  claude-sonnet-5.5: "openai:stub"
  claude-opus-5.5: "openai:stub"
EOF

  echo "==> Deploying Convergence via 'convergence cluster install'..."
  "$BIN" cluster install --namespace="${NAMESPACE}" --provider=direct --openai-key=mock-key --anthropic-key=mock-key \
    --models-file="${MODELS_FILE}" \
    --openai-base-url="http://stub-upstream.${STUB_NS}.svc.cluster.local:8080/v1"

  echo "==> Waiting for Deployments..."
  for d in stand-in pinball agentgateway; do
    kubectl -n "${NAMESPACE}" rollout status "deployment/${d}" --timeout=300s
  done
  kubectl -n "${NAMESPACE}" wait --for=condition=Ready pod --all --timeout=300s

  echo "==> Checking 'convergence cluster status'..."
  "$BIN" cluster status --namespace="${NAMESPACE}"

  kubectl -n "${NAMESPACE}" port-forward svc/pinball 19201:9201 >/dev/null 2>&1 &
  PF_PIDS+=($!)
  kubectl -n "${NAMESPACE}" port-forward svc/agentgateway 13000:3000 >/dev/null 2>&1 &
  PF_PIDS+=($!)

  echo "==> Gateway GET /v1/models must return 200..."
  CODE=000
  for _ in $(seq 1 30); do
    CODE="$(curl -s -o /tmp/models.out -w '%{http_code}' http://localhost:13000/v1/models || true)"
    [ "$CODE" = "200" ] && break
    sleep 2
  done
  echo "models status: ${CODE}"
  [ "$CODE" = "200" ]

  echo "==> Pinball /check must return JSON with a 'model' key..."
  PINBALL_CHECK=""
  for _ in $(seq 1 30); do
    PINBALL_CHECK="$(curl -sf -X POST http://localhost:19201/check \
      -H "Content-Type: application/json" \
      -d '{"model":"auto","messages":[{"role":"user","content":"Investigate memory leak"}]}' || true)"
    [ -n "$PINBALL_CHECK" ] && break
    sleep 2
  done
  echo "Pinball response: ${PINBALL_CHECK}"
  echo "${PINBALL_CHECK}" | python3 -c 'import json,sys; assert "model" in json.load(sys.stdin)'

  kubectl -n "${STUB_NS}" port-forward svc/stub-upstream ${STUB_PORT}:8080 >/dev/null 2>&1 &
  PF_PIDS+=($!)
  for _ in $(seq 1 20); do
    curl -sf http://localhost:${STUB_PORT}/v1/models >/dev/null && break
    sleep 1
  done

  TOKEN="ghp_$(python3 -c 'import secrets,string;print("".join(secrets.choice(string.ascii_letters+string.digits) for _ in range(36)))')"
  chat() {
    curl -s -D /tmp/chat.hdr -o /tmp/chat.out -w '%{http_code}' -X POST http://localhost:13000/v1/chat/completions \
      -H 'Content-Type: application/json' \
      -d "{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"my token is ${TOKEN} please echo\"}]}" || true
  }

  echo "==> Real chat request through gateway (secret must be scrubbed upstream, restored in reply)..."
  CODE=000
  for _ in $(seq 1 20); do
    CODE="$(chat)"
    [ "$CODE" = "200" ] && break
    sleep 3
  done
  echo "chat status: ${CODE}"; cat /tmp/chat.out; echo
  [ "$CODE" = "200" ]

  RECEIVED=""
  for _ in $(seq 1 15); do
    RECEIVED="$(curl -sf http://localhost:${STUB_PORT}/_received || true)"
    [ -n "$RECEIVED" ] && break
    sleep 1
  done
  echo "(a) stub received a placeholder and not the raw token"
  echo "$RECEIVED" | grep -q '<<secret-'
  if echo "$RECEIVED" | grep -qF "$TOKEN"; then echo "ERROR: raw token reached upstream" >&2; exit 1; fi
  echo "(b) response contains the original token"
  grep -qF "$TOKEN" /tmp/chat.out

  # Metrics are per replica: port-forward every pod of an app and sum the series.
  sum_metric() { # app port metric
    local total=0 pod port v
    for pod in $(kubectl -n "${NAMESPACE}" get pods -l "app=$1" -o name); do
      port="$(free_port)"
      kubectl -n "${NAMESPACE}" port-forward "${pod}" "${port}:$2" >/dev/null 2>&1 &
      local pid=$!
      for _ in $(seq 1 20); do curl -sf "http://localhost:${port}/metrics" >/dev/null && break; sleep 0.5; done
      v="$(curl -sf "http://localhost:${port}/metrics" | awk -v m="$3" '$1 ~ ("^" m "(\\{|$)") {s+=$NF} END {print s+0}')"
      kill "${pid}" 2>/dev/null || true
      total="$(python3 -c "print(int(${total}+${v}))")"
    done
    echo "${total}"
  }
  echo "(b2) response carries Pinball's pick as headers"
  grep -qi '^x-pinball-model: .\+' /tmp/chat.hdr || { echo "ERROR: x-pinball-model header missing" >&2; cat /tmp/chat.hdr >&2; exit 1; }
  grep -i '^x-pinball-' /tmp/chat.hdr
  echo "(c) stand-in metrics"
  M="$(sum_metric stand-in 9102 standin_matches_total)"; R="$(sum_metric stand-in 9102 standin_restored_total)"
  echo "matches=${M} restored=${R}"
  [ "${M}" -gt 0 ] && [ "${R}" -gt 0 ]
  echo "(d) pinball decisions"
  D="$(sum_metric pinball 9201 pinball_decisions_total)"
  echo "pinball_decisions_total=${D}"
  [ "${D}" -gt 0 ]

  echo "==> Fail-closed: scale stand-in to 0; request must not reach upstream unscrubbed..."
  BEFORE="$(curl -sf http://localhost:${STUB_PORT}/_received | wc -l)"
  kubectl -n "${NAMESPACE}" scale deployment/stand-in --replicas=0
  kubectl -n "${NAMESPACE}" wait --for=delete pod -l app=stand-in --timeout=120s
  sleep 3
  CODE="$(chat)"
  echo "fail-closed chat status: ${CODE}"; cat /tmp/chat.out; echo
  AFTER="$(curl -sf http://localhost:${STUB_PORT}/_received | wc -l)"
  if grep -qF "$TOKEN" /tmp/chat.out; then echo "ERROR: raw token echoed with Stand-In down" >&2; exit 1; fi
  case "$CODE" in 2*) echo "ERROR: request succeeded with Stand-In down" >&2; exit 1;; esac
  [ "$AFTER" = "$BEFORE" ] || { echo "ERROR: upstream received a request while Stand-In was down" >&2; exit 1; }
  kubectl -n "${NAMESPACE}" scale deployment/stand-in --replicas=2
  kubectl -n "${NAMESPACE}" rollout status deployment/stand-in --timeout=300s

  echo "==> Testing 'convergence cluster uninstall'..."
  trap - ERR
  "$BIN" cluster uninstall --namespace="${NAMESPACE}" --yes
  echo "==> In-cluster KinD test PASSED!"
else
  echo "==> No live Kubernetes cluster detected. Skipped live deployment checks (dry-run only)."
fi

echo "==> Cluster E2E test finished successfully!"
