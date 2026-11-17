# Port pool tasks

- [X] T001 Read the operator spec, address-pool and tunnel code, and the
  review on #1001; record the design in spec.md before rewriting.
- [X] T002 Named pools on a shared endpoint (`--port-pool`,
  `--port-pool-template`, Helm `operator.portPools`), with startup validation.
- [X] T003 Per-port records (`gameplane.local/pool-ports`) of each server's
  effective ports, overrides included; allocation from records, serialized
  and uncached.
- [X] T004 `PortAllocation` condition (`Allocated`, `PoolExhausted`) with
  translations and args; no Service and a 30-second retry when exhausted.
- [X] T005 game-direct Service on container ports; relays dial the main
  Service's ports.
- [X] T006 Unit tests and E2E `TestGameServer_PortPoolAllocation` (kind pool
  `e2e-pool`), registered in the operator bucket.
- [X] T007 operator/specs.md, docs/networking.md, docs/install.md, changelog.
- [X] T008 Compile operator and E2E locally without running tests or lint.
- [ ] T009 CI: unit, envtest, E2E, lint and coverage; runtime verification
  is deferred to CI by repository policy.
- [ ] T010 Maintainer review of the design (spec.md) and the PR.
