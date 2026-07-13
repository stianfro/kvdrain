# Architecture

kvdrain is a single client process. It creates a Kubernetes typed client and a KubeVirt typed client from the same REST configuration.

## Components

- `internal/cli` defines Cobra commands, flags, signal handling, and exit codes.
- `internal/kube` performs snapshots, classification, permission checks, eviction requests, node patches, Lease locking, VMIM watches, hotplug checks, and optional metrics reads.
- `internal/coordinator` owns preflight, ordering, concurrency, retry accounting, completion, and diagnostics.
- `internal/state` defines the stable event envelope and transition reducer.
- `internal/render` produces the live ANSI table, append-only text, or NDJSON.

## Safety boundary

The launcher eviction webhook is the boundary between kvdrain and KubeVirt. kvdrain verifies the launcher's VMI controller owner and UID, a KubeVirt-owned blocking PDB, and a server dry-run response before cordoning. It repeats the check after cordoning. The real eviction must return KubeVirt's expected evacuation 429 response. An HTTP 200 response is a safety failure. Every pod eviction carries a UID precondition. kvdrain never creates a VMIM directly.

A Lease in `kube-system` serializes drains for each node. A run that cordons a node records its run ID on the node. Automatic rollback uses JSON Patch tests for the run ID, resource version, and cordon state. Ambiguous ownership leaves the node cordoned.

Normal pod behavior is conservative. Mirror pods, completed pods, and DaemonSet pods are ignored. Pods with a controller owner are evictable. Pods without a controller owner require `--force`. Local `emptyDir` data requires `--delete-emptydir-data`.

## Observation

Drain snapshots are refreshed once per second. Completion requires three empty observations and a final relist. The read-only `watch` command uses the typed VMIM watch API. Drain retry accounting baselines all existing VMIM UIDs and counts only UIDs first observed after that baseline, without comparing client and API-server clocks.

Transfer data comes from a verified `virt-handler` DaemonSet pod in the deployed KubeVirt namespace. Scrapes have a short timeout and an 8 MiB response limit. Non-finite values are discarded. Metrics are optional, and completion depends on Kubernetes and KubeVirt object state.
