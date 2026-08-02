# Roslyn reviewer reliability repair

- Status: Implemented, validated, and independently reviewed
- Date: 2026-08-02

## Failure

Experiment 002 pinned `CWM.RoslynNavigator 0.7.0`. That release configured the
.NET console logger on stdout, the same stream reserved for MCP JSON-RPC.
Codex therefore received log text before the initialize response and dropped
all three specialist starts. Increasing the startup timeout cannot repair a
corrupted protocol stream.

The experiment also installed selected kit skills under `~/.agents/skills`.
Codex exposes skill metadata from that location to the implementation parent,
so the treatment methodology entered implementation context before the
review-only phase.

## Repair

1. Pin Roslyn Navigator `0.8.0`, which includes the upstream stderr-routing fix
   and stable MCP SDK.
2. Add a declarative MCP health check that initializes the server, verifies
   required tools, loads a bundled .NET smoke solution, and calls
   `find_symbol` for a known marker.
3. Retain the official nupkg and extracted executable DLL, verify both exact
   SHA-256 values, and execute only `dotnet <verified DLL>`.
4. Declare and byte-check the complete reviewer asset closure, including the
   security scan reference, while removing unbundled delegation paths.
5. Reject symlink ancestors, canonical escapes, foreign ownership, and
   group/world-writable plugin, artifact, agent, and reviewer asset paths.
6. Run health with the reviewed agent's actual arguments and environment,
   bounded observable process-group cleanup (including leader-first exit), and
   no manifest-supplied executable command.
7. Require Roslyn evidence; generic/text-search fallback produces INCOMPLETE.
8. Require an isolated end-to-end smoke in which the named reviewer registers,
   makes a semantic Roslyn call, and returns its review verdict.

## Deployment boundary

Same-user asset placement does not hide instructions from an implementation
parent. The plugin is safe only in a reviewer-only LXC. Experiment 003 owns the
real phase boundary: it exports immutable commits, destroys and recreates the
container between implementation, review, fix, and re-review, and installs the
plugin only during review phases.

## Launch gate

Do not begin another product A/B run until unit/integration checks, the real
plugin health test, and the named-reviewer smoke all pass from a clean isolated
worker. Preserve the no-plugin control boundary and do not treat executable
presence or a generic reviewer fallback as passing evidence.
