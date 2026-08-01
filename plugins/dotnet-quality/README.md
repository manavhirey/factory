# dotnet-quality plugin

This is the first bundled plugin for Manav's Factory fork. It supplies a pinned
.NET review methodology while keeping third-party provenance, dependencies, and
activation explicit.

The plugin intentionally excludes the upstream kit's Claude plugin, hooks,
templates, formatting-on-edit scripts, and restore-on-edit scripts. The initial
Factory loader treats `prompt.md` as data and does not execute plugin code.

Runtime packaging and installation steps will be added only after the core
plugin loader validates the manifest contract in
`docs/specs/2026-07-31-plugin-system-design.md`.

The tracked custom-agent definition is installed under the treatment worker's
`~/.codex/agents/` directory. The selected kit skills are exposed only in the
treatment LXC under `~/.agents/skills`, so the control worker cannot discover or
implicitly invoke them.
