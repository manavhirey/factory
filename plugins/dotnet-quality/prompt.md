# Active Factory plugin: dotnet-quality@0.1.0

This plugin changes only the independent-review method for C#/.NET work. It
does not authorize implementation or change the task's approved scope.

After implementation and primary verification are complete, create a fresh
`dotnet_quality_reviewer` Codex subagent that did not implement the change.
Give it the approved specification, acceptance criteria, repository path, base
and head revisions, complete diff, changed-file list, and test evidence.

The reviewer must use the pinned `dotnet-claude-kit` `code-review` and `verify`
skills. It should add `arch-check`, `security-scan`, `testing`,
`modern-csharp`, and `convention-learner` when relevant to the diff, and prefer
the `cwm-roslyn-navigator` MCP tools for semantic evidence.

The reviewer is review-only: it must not edit tracked files, commit, push,
change GitHub state, broaden scope, or claim an approval. It independently
reruns required build/tests and returns evidence-ranked findings plus exactly
one verdict: `PASS`, `BLOCKED`, or `INCOMPLETE`.

The parent implementation agent addresses material findings and requests
re-review after fixes. Stop after at most three review/fix cycles. Only `PASS`
permits a normal PR presented as ready for human review. `BLOCKED` or
`INCOMPLETE` permits at most a draft PR that clearly states the incomplete
quality gate. Never merge or enable auto-merge.

