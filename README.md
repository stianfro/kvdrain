# kvdrain

`kvdrain` is a client-only node drain CLI for Kubernetes clusters running KubeVirt. It triggers normal pod evictions, follows `VirtualMachineInstanceMigration` objects, reports transfer and hotplug state, and keeps repeated PDB responses out of the operator's terminal.

> [!WARNING]
> kvdrain is alpha software. Test it in a non-production cluster, run `kvdrain status NODE` first, and keep a second administrative session available. A failed or interrupted drain normally leaves the node cordoned.

No controller, CRD, or in-cluster component is installed. The CLI uses the selected kubeconfig and context.

## Features

- Read-only drain readiness checks for VMI migratability, effective eviction strategy, node constraints, hotplug volumes, normal pods, and PDBs.
- Server dry-run eviction checks before and after cordoning, so a launcher is not deleted unless KubeVirt confirms migration handling.
- Kubernetes `policy/v1` eviction for normal pods with collapsed PDB status.
- Typed VMIM observation with target, phase, retry history, transfer metrics when available, and hotplug verification.
- TTY table, append-only `--no-tty` output, and versioned NDJSON events.
- Cordon and uncordon through server-side apply with field manager `kvdrain`.

Capacity simulation, scheduler dry-run, and metrics export are not part of the current release.

## Compatibility

The client is built against KubeVirt 1.4 APIs and supports KubeVirt 1.4 through 1.8, including OpenShift Virtualization 4.18. It uses stable Kubernetes and KubeVirt APIs. CI tests the baseline dependencies and compiles the suite against KubeVirt 1.8.4. Guarded lab checks cover a live cluster.

Transfer metrics are optional. kvdrain attempts to read the source node's `virt-handler` metrics through the pod proxy. The table reports `N/A` if the endpoint, metric series, or permission is unavailable.

## Install

Download an archive from [GitHub Releases](https://github.com/stianfro/kvdrain/releases), verify `checksums.txt`, then place `kvdrain` on your `PATH`.

Build from source with Go 1.25.12 or newer:

```sh
git clone https://github.com/stianfro/kvdrain.git
cd kvdrain
just build
install .cache/bin/kvdrain ~/.local/bin/kvdrain
```

## Usage

Inspect a node before making changes:

```sh
kvdrain status worker-3 -o wide
```

Drain it:

```sh
kvdrain drain worker-3 --timeout 45m --retries 1
```

Follow migrations without making changes:

```sh
kvdrain watch worker-3
kvdrain watch worker-3 worker-4 --no-tty
```

Restore scheduling:

```sh
kvdrain uncordon worker-3
```

Global flags:

```text
--context NAME
--kubeconfig PATH
--no-tty
--json
-o wide
```

Drain flags mirror the safety choices that matter for this client:

```text
--timeout 45m
--retries 1
--parallel-outbound N
--force
--delete-emptydir-data
--grace-period N
--abort-uncordons
```

`--parallel-outbound` can only lower the KubeVirt cluster limit. If that setting cannot be read, kvdrain uses 2. `--force` permits unmanaged pod eviction. `--delete-emptydir-data` accepts local `emptyDir` data loss.

## Drain behavior

1. Check API permissions and take a source-node snapshot.
2. Refuse hard blockers before cordoning. Examples include non-migratable VMIs, an unsafe eviction strategy, no eligible target, unmanaged pods, and `emptyDir` without explicit consent.
3. Send dry-run launcher evictions and require KubeVirt's migration response.
4. Cordon with server-side apply, then repeat the safety checks.
5. Evict normal pods, then launchers, subject to the outbound migration limit.
6. Follow VMIM history from the run cutoff. KubeVirt owns replacement VMIM creation. kvdrain observes immutable failed attempts and stops when the retry budget is exceeded.
7. Finish only after source VMIs, active VMIMs, and eligible source pods are gone, and hotplug volumes are ready on their target nodes.

On the first interrupt, kvdrain stops new evictions and waits for active migrations to settle. A second interrupt exits immediately. `--abort-uncordons` restores a node only when this run cordoned it, no source VMI remains, and the interrupted work has settled.

## Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Completed successfully |
| 1 | Preflight blocker, permission error, API error, retry failure, or hotplug verification failure |
| 2 | Invalid command or flag |
| 124 | Timeout, node remains cordoned |
| 130 | Interrupted |

## Automation

`--json` emits one JSON object per state transition. The envelope is versioned as `kvdrain.io/v1alpha1`. See [docs/json-events.md](docs/json-events.md).

```sh
kvdrain watch --json | jq -c 'select(.type == "migration")'
```

## Access and operations

- [RBAC and API access](docs/rbac.md)
- [Architecture](docs/architecture.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Guarded lab checks](docs/lab-e2e.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## AI development notice

The initial implementation and project scaffolding were created by OpenAI GPT-5.6 Codex under human direction. The maintainer reviewed the design, safety decisions, tests, documentation, and resulting code. AI involvement does not replace normal security review, testing, or operator responsibility when using this tool on a cluster.

## License

Apache License 2.0. See [LICENSE](LICENSE).
