# Guarded lab checks

The default lab kubeconfig path is `../lab/kubeconfig`. Read-only checks are safe to run directly:

```sh
just lab-smoke NODE
```

The drain harness is intentionally difficult to invoke by accident. It requires an explicit node and a confirmation value bound to the kubeconfig context, API server, and node. Run it once without the variable to print the required value:

```sh
just lab-e2e worker-3
KVDRAIN_E2E_CONFIRM="VALUE_PRINTED_ABOVE" just lab-e2e worker-3
```

The script requires `jq`, rejects control-plane nodes, records the original node UID and cordon state, and captures the exact drain run ID from NDJSON. Cleanup only uncordons when a conditional JSON Patch confirms that node UID, run owner, current resource version, and cordon state. Any ambiguity leaves the node cordoned. The script does not make an unsafe VMI migratable or bypass normal kvdrain blockers. Use only a disposable lab node whose workloads can move.
