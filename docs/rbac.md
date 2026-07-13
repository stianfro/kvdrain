# RBAC and API access

kvdrain checks its permissions with `SelfSubjectAccessReview` before running a command. The caller needs `create` on `authorization.k8s.io/selfsubjectaccessreviews`. Use existing cluster administration roles where possible. Scope custom access according to your cluster policy.

Read-only status needs:

- `get,list` on nodes
- `list` on pods, persistent volumes, persistent volume claims, and `policy/poddisruptionbudgets`
- `get,list` on KubeVirt VMIs as requested by the command
- `list` on VMIMs and KubeVirt resources
- `get` on VirtualMachines

`watch` needs `list,watch` on `kubevirt.io/virtualmachineinstancemigrations`.

Drain also needs:

- `patch` on nodes
- `create` on `pods/eviction`
- `list` on events for timeout diagnostics
- `get,create,delete` on `coordination.k8s.io/leases` in `kube-system` for the per-node drain lock

Optional transfer metrics need `get` on pods and `pods/proxy`, plus `get` on `apps/daemonsets`, in the KubeVirt installation namespace. Missing metrics access does not fail a drain.

Uncordon needs `patch` on nodes. The API server must allow `authorization.k8s.io/selfsubjectaccessreviews` so the preflight can run.

Avoid storing credentials in command output or issue reports. kvdrain follows normal kubeconfig loading rules and supports explicit `--kubeconfig` and `--context` values.
