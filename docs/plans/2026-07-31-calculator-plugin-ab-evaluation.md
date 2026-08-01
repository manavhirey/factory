# Calculator Plugin A/B Evaluation Plan

- Status: Proposed; plugin implemented, execution assets and LXC provisioning
  pending
- Date: 2026-07-31
- Question: Does the `dotnet-quality` plugin measurably reduce dead or smelly
  code, improve .NET best-practice adherence, reliability, or runtime
  efficiency, produce a better UI and smoother user experience, and ultimately
  reduce defects that reach the user enough to justify its cost and complexity?

## Experimental unit

Create one immutable local Git seed, copy it into each isolated LXC, and give
both arms the same approved specification:

- **Control:** Factory fork, `enabled_plugins = []`
- **Treatment:** same Factory fork commit,
  `enabled_plugins = ["dotnet-quality"]`

The first pair is a pilot of the evaluation loop. It can reveal integration
failures and large effects, but one pair is not enough to establish a general
quality improvement. If the loop works, repeat it with at least three task
seeds before making an adoption decision.

## Calculator specification

Build a small ASP.NET Core 10 Razor Pages calculator targeting `net10.0`.

Required behavior:

- provide a complete, visually coherent web front end rather than an API or
  command-line interface;
- add, subtract, multiply, and divide two decimal operands;
- server-side validation for missing and invalid operands;
- a clear divide-by-zero result without an unhandled exception;
- responsive layouts without horizontal scrolling at narrow mobile, tablet,
  and desktop widths;
- clear visual hierarchy, operation controls, input, result, validation, focus,
  hover, active, disabled, and error states;
- preserve entered values and provide immediate, understandable feedback after
  success or failure without jarring layout shifts or duplicate submissions;
- keyboard-operable, semantically labeled controls, visible focus, readable
  contrast, and result/error announcements suitable for assistive technology;
- no persistence, authentication, external services, JavaScript framework, or
  deployment work; small progressive enhancements may use platform JavaScript,
  but the core calculation flow must remain usable when it is unavailable;
- one application project and one xUnit test project;
- behavioral tests for all four operations, invalid input, and divide by zero;
- `dotnet restore --locked-mode`, build, test, format verification, and publish;
- the evaluator runs the same deterministic command sequence from a pinned SDK
  setup in each container.

The direct task payload, starting tree, commit metadata, and authorization
sequence must be byte-identical across arms. Infrastructure identities such as
VMID and hostname are recorded separately and excluded from the task payload.

## Repository model

This pilot does not use hosted calculator repositories, GitHub issues, pull
requests, CI, or branch protection. Each LXC receives the same local bare seed
origin and a working clone at the same paths. Factory registers that clone as a
local worker repository, receives the task directly through its control plane,
and may publish the result branch only to the container-local bare origin. The
origin and all worktrees are disposable experiment artifacts with no external
remote.

## Controlled variables

Pin and record for both arms:

- Factory fork commit and binary hashes;
- Codex CLI version, model, reasoning effort, and authentication identity;
- .NET SDK, NuGet lock, operating-system image, CPU, memory, disk, and timeout;
- worker concurrency of one and no retained worktree;
- implementation prompt, acceptance criteria, and human approvals;
- starting Git commit, local bare-origin contents, paths, and repository
  settings;
- network policy and available commands;
- independent evaluator version and rubric.

Use two fresh Proxmox LXCs from the same Ubuntu 24.04 template and byte-matched
provisioning contract. This avoids duplicating source-container credentials,
worker IDs, caches, and SSH host keys. Install equivalent credentials
separately. Run the pair in a randomized order, close enough in time to limit
model-service drift, and do not tune prompts or plugin contents after the first
arm starts.

## Treatment boundary

The control still receives the baseline generic independent reviewer required
by Factory policy. The treatment replaces only that review method with the
`dotnet-quality` plugin's pinned reviewer and bounded fix/re-review loop. The
implementation instructions before review are otherwise identical.

This isolates the value of the specialist quality loop. A later experiment may
test whether giving the kit to the implementation agent improves first-pass
generation quality.

## Independent evaluation

Neither Factory result nor plugin verdict is the outcome measure. After both
arms finish, copy the final commits into evaluator-owned directories named A
and B, remove Factory/plugin labels from the evaluator input, and apply the same
external harness.

### Primary value outcomes

Measure each outcome independently. Do not use one aggregate score to hide a
regression in another outcome.

| Outcome | Evidence | Better result |
|---|---|---|
| Dead and smelly code | Evaluator-owned compiler/analyzer diagnostics, unused or unreachable code, duplication, avoidable complexity, needless abstraction, and adjudicated false positives; report absolute counts and counts normalized by production lines of code | Fewer severity-weighted, independently confirmed findings without suppressing diagnostics |
| Best-practice adherence | Blinded review against the frozen C#/.NET conventions, analyzer findings, framework usage, error handling, validation, naming, nullability, and test practices | Fewer violations and a higher evidence-backed rubric score |
| Code reliability | Visible and hidden test results, mutation score, unhandled failures, deterministic repeat runs, and escaped defects found after the Factory result is frozen | More meaningful fault detection and fewer severity-weighted escaped defects |
| Runtime efficiency | Evaluator-owned repeatable benchmarks for request latency, allocations, and memory after correctness is established | A repeatable material improvement outside measured noise, with no readability or reliability regression |
| UI quality | Blinded screenshots and interaction review across frozen mobile, tablet, and desktop viewports; visual hierarchy, consistency, responsive behavior, accessibility, and state design | A more coherent, accessible, responsive, and polished interface without needless front-end complexity |
| Experience reliability and smoothness | Repeated browser task completion, input preservation, focus behavior, layout stability, duplicate-submit protection, client/server errors, perceived responsiveness, and blinded user ratings | Fewer interaction failures and retries, stable feedback, and higher user-rated smoothness and trust |
| User defect burden | Unique defects reported by Manav after handoff, severity, whether Manav had to diagnose or fix them, time to resolution, and reopened defects | Fewer and less severe user-reported or user-fixed defects |

The calculator pilot can measure the first six outcomes and can approximate the
last with a blinded human acceptance session. A credible user-defect claim
requires a later longitudinal personal-use trial across multiple real tasks;
the pilot must label its escaped-defect result as a proxy rather than claiming
field reliability.

### Deterministic gates

- clean locked restore;
- build with zero errors and recorded warnings;
- full visible test suite;
- evaluator-owned hidden behavioral tests;
- evaluator-owned static analysis with identical rules and no arm-specific
  suppressions;
- evaluator-owned mutation and repeatability checks;
- `dotnet format --verify-no-changes`;
- vulnerable transitive package scan;
- publish and start the application;
- HTTP/browser smoke for success, validation, and divide-by-zero paths;
- evaluator-owned screenshots at the frozen narrow-mobile, tablet, and desktop
  viewports, with no clipping or horizontal overflow;
- automated accessibility checks plus keyboard, focus, and result-announcement
  verification;
- repeated browser interaction sequences with no console errors, failed
  requests, duplicate submissions, stale results, or unexpected input loss;
- repeatable latency, allocation, and memory measurements using the same
  benchmark inputs and warm-up policy;
- no secrets, generated artifacts, or unrelated files in Git.

Hidden tests should cover decimal signs and scale, whitespace/invalid form
input, division by zero, overflow behavior, repeated submissions, and all four
operations without exposing those exact cases to either implementation agent.

### Blinded rubric

Score each arm from 0–3 on:

1. acceptance-criteria correctness;
2. edge-case behavior;
3. test quality and meaningful negative coverage;
4. absence of dead code and independently confirmed code smells;
5. maintainability and clarity;
6. idiomatic modern C#/.NET and best-practice adherence;
7. architecture proportionality and absence of needless abstraction;
8. reliability and fault-detection strength;
9. security/input-handling posture;
10. visual design quality and responsive polish;
11. usability, accessibility, and error recovery;
12. perceived responsiveness, interaction smoothness, and trust;
13. runtime efficiency without quality tradeoffs;
14. reproducible build quality and scope discipline.

The evaluator reports concrete file/line evidence before the arm identities are
revealed. Human review then records any defects the automated evaluator missed.

### Blinded user assessment

After automated evaluation is frozen and before arm identities are revealed,
present Manav with neutral builds labeled A and B in randomized order. Do not
show source, plugin logs, implementation metadata, or container names. Keep the
task script, browser, viewport sizes, data, and available time identical.

Manav completes these tasks in both arms:

1. calculate one result for each operation;
2. use negative and decimal operands;
3. trigger invalid-input and divide-by-zero handling, then recover;
4. complete the primary flow using only the keyboard;
5. inspect and use the calculator at mobile and desktop sizes; and
6. repeat calculations quickly enough to expose stale results, duplicate
   submissions, input loss, or disruptive layout changes.

Immediately after each arm, capture a 1–5 rating and optional comments for:

- visual appeal and coherence;
- clarity and ease of use;
- responsive/mobile quality;
- perceived speed and smoothness;
- feedback and error recovery;
- reliability and trust; and
- overall experience.

Before reveal, ask for A, B, or tie; preference confidence from 1–5; the main
reason; what felt frustrating; and every defect encountered. Record observed
task failures in the defect ledger independently of subjective ratings. User
opinion is primary evidence for UI and experience quality, but it cannot
override a deterministic correctness, accessibility, security, or safety
failure. One person's preference is reported as within-person evidence, not a
claim about all users.

### Defect ledger

Assign every defect a stable ID, severity, discovery stage, affected arm, and
status. Distinguish:

- findings caught and fixed by the implementation or review loop before freeze;
- escaped defects found by the hidden evaluator after freeze;
- defects Manav reports during blinded acceptance or later personal use;
- defects Manav must diagnose or fix himself; and
- false positives or suggestions that caused churn without improving behavior.

Reviewer finding volume alone is not a benefit. Report confirmed defect yield,
false-positive rate, fix effectiveness, regressions introduced by fixes, and
which final defects escaped the plugin.

## Operational and cost measures

Record independently of the blinded quality score:

- wall-clock time from authorization to frozen result;
- Codex tokens/cost when available;
- number and severity of reviewer findings;
- confirmed reviewer defect yield and false-positive rate;
- number of fix/re-review cycles;
- code churn caused by review fixes and any unnecessary abstractions added;
- browser task completion time, retries, and interaction failures;
- tool failures, retries, and human interventions;
- evaluator readiness, diff size, test count, and changed-file count;
- plugin setup and maintenance time.

## Decision rule

The calculator pair is only a pilot. The plugin advances to repeated trials
when the treatment:

- violates no human gate, scope boundary, or external-side-effect boundary;
- passes all deterministic gates;
- has no material regression in dead/smelly code, best-practice adherence,
  reliability, runtime efficiency, UI quality, user experience, security,
  maintainability, accessibility, or scope;
- shows a concrete, evidence-backed gain in at least one of dead/smelly code,
  best-practice adherence, reliability, runtime efficiency, UI quality,
  experience smoothness, or escaped-defect burden; and
- does not add more than 50% wall-clock/token cost without a material quality
  benefit.

After at least three matched pairs, regard it as an adoption candidate only if
the median treatment result improves by at least 10% in one or more primary
value outcomes, no primary outcome or safety property materially regresses, and
operational overhead remains acceptable. Regard it as a great Factory addition
only after the later personal-use defect ledger also shows reduced user-reported
or user-fixed defect burden, or the repeated trials show a strong reliability
gain with no field regression. Otherwise revise or reject the plugin rather
than moving thresholds after observing results.

### Required decision report

Produce a treatment-minus-control report containing raw measurements,
normalized deltas, concrete code examples, uncertainty and evaluator caveats,
confirmed benefits, screenshots, blinded user ratings and preference, and
costs. The costs section must include added runtime, tokens, setup and
maintenance work, false positives, code churn, tool failures, and signs of
overengineering. End with one recommendation: adopt, continue testing, revise,
or reject.

## Execution sequence

1. Implement and test the bundled prompt-plugin loader in the Factory fork.
2. Build the `dotnet-quality` bundle at pinned third-party revisions.
3. Freeze the primary-outcome scorecard, defect taxonomy, calculator seed, and
   evaluator-owned hidden harness before either arm runs.
4. Prepare two clean, equivalent Factory deployments from the same LXC template
   and provisioning inputs.
5. Validate an empty control plugin set and one treatment plugin set in worker
   health, prompt snapshots, logs, and manifests.
6. Randomize run order, record the assignment privately, and run both arms.
7. Freeze final commits and perform the automated blinded evaluation.
8. Conduct Manav's blinded A/B user assessment, freeze his ratings, preference,
   comments, and reported defects, then reveal the assignment.
9. Produce the required pros/cons decision report and decide whether to repeat,
   revise, or stop.
10. If repeated trials justify it, run a separate longitudinal personal-use phase
   to measure user-reported and user-fixed defects before calling the plugin a
   great Factory addition.

## Precondition requiring Manav's decision

- explicit approval of the final calculator specification before either
  implementation run. No hosted calculator repository decision is required.

## Provisioned experiment containers

Manav explicitly authorized two separate containers. They were created fresh
from `ubuntu-24.04-standard_24.04-2_amd64.tar.zst` and verified stopped:

| Arm | VMID | Hostname | Profile |
|---|---:|---|---|
| Control | 102 | `factory-control` | 4 CPU, 6144 MiB RAM, 2048 MiB swap, 64 GiB disk |
| Treatment | 103 | `factory-dotnet-quality` | 4 CPU, 6144 MiB RAM, 2048 MiB swap, 64 GiB disk |

Both are unprivileged, use firewall-enabled DHCP networking, have `onboot=0`,
and have no nesting or optional container features. Only identity-bearing fields
such as VMID, hostname, MAC address, and disk volume differ.
