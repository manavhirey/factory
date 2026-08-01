# Factory documentation

Start with the root [README](../README.md) to run Factory.

## Current implementation

- [Architecture](../ARCHITECTURE.md): system boundaries, flows, contracts,
  security, limits, and source map.
- [Local guide](local.md): build, configure, start, delegate, and troubleshoot.
- [Worker contract](worker.md): identity, runtimes, claiming, process safety,
  and worktree cleanup.
- [Issue poller](poller.md): GitHub queues, provider command adapters,
  deduplication, and operation.
- [Security policy](../SECURITY.md): reporting and the current trust model.
- [Contributing](../CONTRIBUTING.md): setup, checks, and pull request standards.

## Fork extensions

- [Bundled plugin system](specs/2026-07-31-plugin-system-design.md): explicit
  worker activation, manifest/provenance validation, prompt composition, and
  current MVP limits.
- [Calculator plugin A/B plan](plans/2026-07-31-calculator-plugin-ab-evaluation.md):
  matched control/treatment design for the first `dotnet-quality` evaluation.

## Proposed designs

These documents describe work that is not implemented:

- [Reusable workflows and automations](workflows/design.md)
- [Advanced GitHub ingest design](github-ingest/design.md)
- [Unified CLI](cli/design.md)

Current behavior belongs in the root `ARCHITECTURE.md`. Proposed behavior belongs
in a focused design until it is implemented.
