# Guarded lab checks

The default lab kubeconfig path is `../lab/kubeconfig`. Read-only checks are safe to run directly:

```sh
just lab-smoke NODE
```

The drain harness is intentionally difficult to invoke by accident. It requires an explicit node and an exact confirmation value:

```sh
KVDRAIN_E2E_CONFIRM="drain-worker-3" just lab-e2e worker-3
```

The script rejects control-plane nodes, records the original cordon state, and restores that state on exit. It does not make an unsafe VMI migratable or bypass normal kvdrain blockers. Use only a disposable lab node whose workloads can move.
