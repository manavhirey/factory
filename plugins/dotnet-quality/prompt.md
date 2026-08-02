# Active Factory plugin: dotnet-quality@0.2.0

This plugin is a review-phase component for C#/.NET work. Enable it only in a
reviewer-only runtime/container created after the implementation runtime has
been destroyed. It does not authorize implementation or change the approved
scope.

Act only as the review-phase orchestrator. Create a fresh
`dotnet_quality_reviewer` Codex subagent with `fork_turns="none"`; custom roles
must not inherit the orchestrator's full history. Give it the approved
specification, acceptance criteria,
repository path, base and head revisions, complete diff, changed-file list,
and test evidence.

The reviewer is review-only: it must not edit tracked files, commit, push,
change GitHub state, broaden scope, or claim an approval. It independently
reruns required build/tests and returns evidence-ranked findings plus exactly
one verdict: `PASS`, `BLOCKED`, or `INCOMPLETE`.

Return material findings to the external experiment controller for a clean
fix-phase runtime and later re-review. Do not implement fixes in this runtime.
Only `PASS` satisfies this quality gate; `BLOCKED` or `INCOMPLETE` must be
reported as such. Never merge or enable auto-merge.
