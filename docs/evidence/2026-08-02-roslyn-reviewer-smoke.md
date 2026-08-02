# Roslyn reviewer repair — sanitized smoke evidence

- Date: 2026-08-02
- Environment: disposable repository in the completed Experiment 002 treatment
  LXC; no production data, credentials, hosted repository, or external product
  side effect
- Branch under test: `codex/fix-roslyn-specialist-reviewer`

## Reproduction and root cause

The pinned `CWM.RoslynNavigator 0.7.0` process was reproduced writing .NET host
and MCP transport log lines to stdout while stderr remained empty. stdout is
the MCP JSON-RPC transport, so Codex attempted to parse the first log line as a
protocol response and dropped initialization. This matches the upstream
stderr-routing fix in commit `c91b7e94b45ae4e536cce727e8be9b18d7cb9a18`.
The fix is documented in the upstream `0.7.1` changelog/source, but no `0.7.1`
NuGet package was published. The repair pins published `0.8.0`, which contains it.
The failure is protocol corruption, not startup latency.

The prior treatment also exposed all seven kit skills from the implementation
parent's user-level discovery directory. Moving them below Codex home did not
create a same-user security boundary; that historical smoke proves named-role
behavior, not phase isolation. The repaired deployment therefore requires a
reviewer-only container and external LXC recreation between phases.

## Automated verification

- Focused hardening tests: pass for exact artifact hashes, missing/extra asset
  closure, installed-agent drift, shell/extra-argument rejection, symlink
  ancestors, unsafe modes, Args/Env parity, stdout corruption, notification
  bursts, partial stdout, early EOF, structured semantic success, echoed-marker
  not-found/tool-error responses, malformed/empty tool content, timeout,
  cleanup-error propagation, timeout descendant cleanup, and leader-exit
  descendant cleanup. A Unix ownership unit test rejects foreign owners.
- Real plugin health: pass in 3.09 seconds using the exact agent command after
  matching every extracted runtime file to the retained official nupkg. MCP
  server info reported `CWM.RoslynNavigator 0.8.0.0`; the probe semantically
  resolved `ReviewerHealthMarker` through `find_symbol`.
- Full Go validation: an initial cross-package concurrent run reproduced the
  pre-existing timing-sensitive `TestTimeoutStopsIgnoringProcessGroup` failure.
  The exact test passed alone in 7.77 seconds. A clean package-serial
  `go test -p 1 -count=1 -timeout 5m ./...` then passed, including
  `internal/controlplane` (85.476s) and `internal/worker` (152.930s).
  After complete extracted-package verification was added, one worker rerun
  reproduced the same timing failure; the exact test passed in 6.90 seconds,
  the focused plugin suite passed in 5.881 seconds, and a subsequent clean
  complete worker run passed in 160.047 seconds.
- Go formatting, `go vet ./...`, the worker/control-plane import boundary,
  Node-free tooling, launcher checks, UI lint/typecheck, and all 40 UI tests:
  pass.
- MCP corruption unit test: a simulated stdout log before initialize is
  rejected.
- Semantic probe unit test: initialize, tools/list, and tools/call are all
  required.
- Asset tests: missing/drifted agent or reviewer assets, missing transitive
  assets, symlink ancestors, foreign ownership, and unsafe modes fail closed.
- Pinned artifact verification: official nupkg SHA-256
  `18432346439a1f1a1fdc1b82f7f944a4e6f4202347a2cfece7ee75992530cfcd` and
  executable DLL SHA-256
  `becde1c2c2b4a478f099d16f5c5dc426365fa4c7d8c0d656fe7e031086b2bb5d`.

The reviewer-only validation LXC resolved `/usr/bin/dotnet` version `10.0.110`
(SHA-256 `a2e03e682b5ba32303077bc5ed95ca3dd6b57b6d55d09491b67444644e211940`).
Factory scanned the canonical plugin bundle, `/opt/factory/plugin-artifacts`,
and the installed agent/reviewer trees below Codex home. Validation files were
worker-owned mode `0600` and directories mode `0700`. The reviewed agent plus
sorted reviewer-asset hash-list digest was
`5e5eda0e3a0200be24c2d75ec56aaf1f9c551a83f113548891bfb6bddb6ca35d`;
the manifest remains the canonical complete list of its eight asset files.

## Independent re-review

An independent agent found two material issues in the first hardened patch: a
raw-string semantic success check that could accept an echoed query in an error,
and descendant leakage when the MCP group leader exited before cleanup. The
repair now parses `CallToolResult`, rejects JSON-RPC/tool errors and malformed
content, and requires positive counts plus an exact symbol in the structured
Roslyn result. Cleanup now handles leader-first exit and makes cleanup failure
part of the health error. The independent rerun passed the focused Roslyn suite
in 4.508 seconds, formatting, and `go vet ./...`; it independently matched both
official hashes. Its Docker full-suite child-polling failures reproduced on the
unchanged baseline, confirming that environment-sensitive failure was not
introduced by this patch.

## Named specialist smoke

The first post-repair named-agent smoke successfully reviewed the disposable
diff and used Roslyn, but its parent initially attempted a full-history custom
role fork. Codex rejected that launch before agent creation, and the parent
retried with isolated context. The orchestration prompt was then corrected to
require `fork_turns="none"` explicitly.

The final bounded rerun exited zero with an empty stderr trace and no launch
retry. The named `dotnet_quality_reviewer` loaded its reviewed instructions,
built the disposable project with zero warnings and errors, and returned:

```text
NAMED_REVIEWER: dotnet_quality_reviewer — no findings; build passed with 0 warnings and 0 errors.
ROSLYN_SEMANTIC_EVIDENCE: cwm_roslyn_navigator.get_symbol_detail resolved public ReviewerHealthMarker with Describe(): string; get_symbol_source confirmed it returns exactly "reviewer semantic smoke".
VERDICT: PASS — bounded diff is review-only; no tracked worktree changes.
```

Only ordinary `bin/` and `obj/` build outputs were created in the disposable
repository; tracked and staged diffs remained empty. Raw runtime logs remain
outside the repository and contain no evidence needed for review.
