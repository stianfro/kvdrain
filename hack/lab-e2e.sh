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
if [[ ! -f "$kubeconfig" ]]; then
  echo "kubeconfig not found: $kubeconfig" >&2
  exit 2
fi

context=$($kubectl --kubeconfig "$kubeconfig" config current-context)
server=$($kubectl --kubeconfig "$kubeconfig" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
cluster_id=$(printf '%s' "$context|$server" | sha256sum | cut -c1-12)
confirmation="drain-$context-$node-$cluster_id"
if [[ ${KVDRAIN_E2E_CONFIRM:-} != "$confirmation" ]]; then
  echo "set KVDRAIN_E2E_CONFIRM=$confirmation to run the mutating lab check" >&2
  exit 2
fi

labels=$($kubectl --kubeconfig "$kubeconfig" get node "$node" -o go-template='{{range $key, $value := .metadata.labels}}{{printf "%s\n" $key}}{{end}}')
if grep -Eq '^node-role.kubernetes.io/(control-plane|master)$' <<<"$labels"; then
  echo "refusing to drain control-plane node $node" >&2
  exit 2
fi

initial_node=$($kubectl --kubeconfig "$kubeconfig" get node "$node" -o json)
initial=$(jq -r '.spec.unschedulable // false' <<<"$initial_node")
initial_uid=$(jq -r '.metadata.uid' <<<"$initial_node")
expected_owner=""
restore() {
  if [[ "$initial" == "true" ]]; then
	return
  fi
	current=$($kubectl --kubeconfig "$kubeconfig" get node "$node" -o json)
	rv=$(jq -r '.metadata.resourceVersion' <<<"$current")
	unschedulable=$(jq -r '.spec.unschedulable // false' <<<"$current")
	if [[ "$unschedulable" != "true" ]]; then
		return
	fi
	if [[ -z "$expected_owner" ]]; then
		echo "the drain run ID was not captured, leaving node $node cordoned" >&2
		return
	fi
	patch=$(jq -cn --arg uid "$initial_uid" --arg rv "$rv" --arg owner "$expected_owner" '[
		{"op":"test","path":"/metadata/uid","value":$uid},
		{"op":"test","path":"/metadata/resourceVersion","value":$rv},
		{"op":"test","path":"/metadata/annotations/kvdrain.io~1cordon-owner","value":$owner},
		{"op":"test","path":"/spec/unschedulable","value":true},
		{"op":"replace","path":"/spec/unschedulable","value":false},
		{"op":"remove","path":"/metadata/annotations/kvdrain.io~1cordon-owner"}
	]')
	if ! $kubectl --kubeconfig "$kubeconfig" patch node "$node" --type=json -p "$patch" >/dev/null; then
		echo "node $node changed during cleanup, leaving it cordoned" >&2
	fi
}
trap restore EXIT

"$binary" --kubeconfig "$kubeconfig" --no-tty status "$node"
events=$(mktemp)
set +e
"$binary" --kubeconfig "$kubeconfig" --json drain "$node" --timeout "${KVDRAIN_E2E_TIMEOUT:-45m}" | tee "$events"
drain_rc=${PIPESTATUS[0]}
set -e
expected_owner=$(jq -sr 'first(.[] | select(.runID != null and .runID != "") | .runID) // ""' "$events")
rm -f "$events"
exit "$drain_rc"
