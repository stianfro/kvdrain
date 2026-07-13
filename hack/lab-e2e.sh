#!/usr/bin/env bash
set -euo pipefail

node=${1:-}
kubeconfig=${2:-../lab/kubeconfig}
kubectl=${KUBECTL:-kubectl}
binary=${KVDRAIN_BINARY:-.cache/bin/kvdrain}

if [[ -z "$node" ]]; then
  echo "an explicit node is required" >&2
  exit 2
fi
if [[ ${KVDRAIN_E2E_CONFIRM:-} != "drain-$node" ]]; then
  echo "set KVDRAIN_E2E_CONFIRM=drain-$node to run the mutating lab check" >&2
  exit 2
fi
if [[ ! -f "$kubeconfig" ]]; then
  echo "kubeconfig not found: $kubeconfig" >&2
  exit 2
fi

labels=$($kubectl --kubeconfig "$kubeconfig" get node "$node" -o go-template='{{range $key, $value := .metadata.labels}}{{printf "%s\n" $key}}{{end}}')
if grep -Eq '^node-role.kubernetes.io/(control-plane|master)$' <<<"$labels"; then
  echo "refusing to drain control-plane node $node" >&2
  exit 2
fi

initial=$($kubectl --kubeconfig "$kubeconfig" get node "$node" -o jsonpath='{.spec.unschedulable}')
restore() {
  if [[ "$initial" == "true" ]]; then
    $kubectl --kubeconfig "$kubeconfig" cordon "$node" >/dev/null
  else
    $kubectl --kubeconfig "$kubeconfig" uncordon "$node" >/dev/null
  fi
}
trap restore EXIT

"$binary" --kubeconfig "$kubeconfig" --no-tty status "$node"
"$binary" --kubeconfig "$kubeconfig" --no-tty drain "$node" --timeout "${KVDRAIN_E2E_TIMEOUT:-45m}"
