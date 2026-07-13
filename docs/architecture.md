# Architecture

kvdrain is a single client process. It creates a Kubernetes typed client and a KubeVirt typed client from the same REST configuration.

## Components

- `internal/cli` defines Cobra commands, flags, signal handling, and exit codes.
- `internal/kube` performs snapshots, classification, permission checks, eviction requests, SSA node patches, VMIM watches, hotplug checks, and optional metrics reads.
- `internal/coordinator` owns preflight, ordering, concurrency, retry accounting, completion, and diagnostics.
- `internal/state` defines the stable event envelope and transition reducer.
- `internal/render` produces the live ANSI table, append-only text, or NDJSON.

## Safety boundary

The launcher eviction webhook is the boundary between kvdrain and KubeVirt. Before cordoning, kvdrain sends a server dry-run eviction and requires the expected KubeVirt response. It repeats the check after cordoning. kvdrain never creates a VMIM directly.

Normal pod behavior is conservative. Mirror pods, completed pods, and DaemonSet pods are ignored. Pods with a controller owner are evictable. Pods without a controller owner require `--force`. Local `emptyDir` data requires `--delete-emptydir-data`.

## Observation

Drain snapshots are refreshed once per second. The read-only `watch` command uses the typed VMIM watch API. VMIM history is filtered by creation time and UID so an older failure is not charged to a new run.

Transfer data comes from `virt-handler` Prometheus metrics through the Kubernetes pod proxy. It is an optional signal. Completion depends on Kubernetes and KubeVirt object state, not metrics availability.
