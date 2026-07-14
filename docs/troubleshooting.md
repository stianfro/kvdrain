# Troubleshooting

## Preflight reports no eligible target

Check node readiness, cordon state, `kubevirt.io/schedulable`, VMI and launcher node selectors, required node affinity, taints and tolerations, and persistent volume node affinity. The check is intentionally conservative and does not simulate every scheduler plugin.

## Dry-run eviction is not confirmed

Confirm that the effective eviction strategy is `LiveMigrate` or `LiveMigrateIfPossible`, KubeVirt's eviction webhook is available, and the launcher belongs to the expected VMI. kvdrain refuses to proceed if the server dry-run could delete the launcher without a migration response.

## A normal pod remains BLOCKED

Use `-o wide` or `--json` to see matching PDB data. kvdrain retries without printing every API response. Fix the application PDB or restore enough healthy replicas.

## Migration stays pending

At timeout, kvdrain reports VMIM conditions, target pod scheduling conditions, and Warning events when permitted. Common causes include resource pressure, taints, volume topology, and pod-count limits.

## Transfer is N/A

The drain can continue. Check `pods/proxy` access, the source node's `virt-handler` pod, port 8443 proxy access, and whether the installed KubeVirt version exports the supported migration metrics.

## Interrupted or timed-out drain

The node remains cordoned by default. Inspect `kvdrain status NODE`, active VMIMs, target launcher pods, and hotplug attachment pods. Use `kvdrain uncordon NODE` only after deciding that new scheduling is safe.

## A new workload appears during drain

A cordon stops normal scheduler placement but does not stop clients that set `spec.nodeName` directly. Stop that automation during maintenance. kvdrain requires a quiet interval and performs a final relist, but direct placement can always race any client-side completion check.

## A drain Lease remains after a client crash

kvdrain does not take over an existing Lease based on wall-clock expiry. Verify that no drain process is active, then delete the named Lease from `kube-system` and retry. This fail-closed behavior prevents a skewed client clock from allowing concurrent drains.
