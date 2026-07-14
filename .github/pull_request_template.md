## Summary

Describe the operator problem and the chosen change.

## Safety

- [ ] I considered launcher eviction, cordon restoration, timeout, and interrupt behavior.
- [ ] I did not weaken preflight checks without explicit tests and rationale.

## Verification

- [ ] Tests cover changed behavior.
- [ ] `just ci` passes.
- [ ] Documentation is updated when output or flags change.
