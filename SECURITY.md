# Security policy

## Reporting

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting for this repository. Include affected versions, impact, and a minimal reproduction when possible.

You should receive an initial response within seven days. Supported releases and the default branch receive security fixes.

## Scope

kvdrain uses the caller's Kubernetes credentials and can evict workloads or patch nodes. Reports involving permission escalation, unsafe launcher deletion, credential exposure, release artifact integrity, or bypassed drain safeguards are in scope.
