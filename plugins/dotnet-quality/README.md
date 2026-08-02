# dotnet-quality plugin

This is the first bundled plugin for Manav's Factory fork. It supplies a pinned
.NET review methodology while keeping third-party provenance, dependencies, and
activation explicit.

The plugin intentionally excludes the upstream kit's Claude plugin, hooks,
templates, formatting-on-edit scripts, and restore-on-edit scripts. Factory
treats `prompt.md` as orchestration data and does not execute plugin code.
The reviewer assets are reviewed derivatives of provenance commit
`cd83d315986c27621da178dad73bd95d503c1540`, not byte-identical upstream files:
unavailable cross-agent/skill delegations were removed and the required
security-layer reference was bundled locally.

## Runtime installation and boundary

This plugin is supported only in a reviewer-only runtime/container. It must not
be enabled in the implementation parent. Experiment 003 provides phase
separation externally by exporting immutable commits and fully recreating each
LXC between implementation, review, and fix phases.

Retain the official `CWM.RoslynNavigator 0.8.0` nupkg at
`/opt/factory/plugin-artifacts/dotnet-quality/CWM.RoslynNavigator.0.8.0.nupkg`,
extract it below the adjacent `CWM.RoslynNavigator.0.8.0/` directory, and make
the entire tree owned by the worker user and not group/world writable. The
manifest pins the official download URL, nupkg SHA-256, extracted tree,
executable DLL path, and DLL SHA-256. Activation compares every extracted file
against the retained nupkg and rejects extra files. Version `0.7.0` is
prohibited because it wrote host logs to
stdout and corrupted MCP JSON-RPC framing.

Copy the tracked custom-agent definition byte-for-byte to
`$CODEX_HOME/agents/dotnet-quality-reviewer.toml` (default Codex home:
`~/.codex`). Copy every path in the manifest's complete `reviewer_assets` list
byte-for-byte below `$CODEX_HOME/reviewer-skills/dotnet-quality/`, preserving
its relative path. This includes the security scan's required layer reference.
The review-phase orchestrator must spawn the custom role with
`fork_turns="none"`; Codex rejects custom roles attached to full-history forks.

Configure both `plugin_directory` and
`plugin_artifact_directory = "/opt/factory/plugin-artifacts"`. The worker fails
closed on artifact/asset drift, symlinks, foreign ownership, unsafe modes, or
agent command/argument/environment mismatch. Preflight uses the agent's actual
`dotnet <pinned DLL>` command, arguments, and environment from the bundled
smoke directory, then completes MCP initialize, tools/list, and a real
`find_symbol` call for `ReviewerHealthMarker`.

For an isolated provisioned-worker smoke check, set
`FACTORY_PLUGIN_REAL_HEALTH=1` and run
`go test -run TestBundledDotnetQualityPluginRealHealth ./internal/worker`.
This check must pass before the worker may claim product work. Separately run a
named `dotnet_quality_reviewer` Codex subagent against a disposable .NET diff
and require evidence from at least one semantic Roslyn call.
