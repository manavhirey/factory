# Calculator Plugin A/B Evaluation Plan

- Status: Proposed; implementation and infrastructure pending
- Date: 2026-07-31
- Question: Does the `dotnet-quality` plugin improve the quality of a small C#
  application enough to justify its cost and complexity?

## Experimental unit

Create two repositories from one immutable starting commit and give them the
same approved specification:

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

- add, subtract, multiply, and divide two decimal operands;
- server-side validation for missing and invalid operands;
- a clear divide-by-zero result without an unhandled exception;
- keyboard-operable, labeled form and readable result/error state;
- no persistence, authentication, external services, JavaScript framework, or
  deployment work;
- one application project and one xUnit test project;
- behavioral tests for all four operations, invalid input, and divide by zero;
- `dotnet restore --locked-mode`, build, test, format verification, and publish;
- GitHub Actions runs the same deterministic checks from a pinned SDK setup.

The full issue/specification text, starting tree, commit metadata, labels, and
authorization sequence must be byte-identical across arms except for repository
identity.

## Controlled variables

Pin and record for both arms:

- Factory fork commit and binary hashes;
- Codex CLI version, model, reasoning effort, and authentication identity;
- .NET SDK, NuGet lock, operating-system image, CPU, memory, disk, and timeout;
- worker concurrency of one and no retained worktree;
- implementation prompt, acceptance criteria, and human approvals;
- starting Git commit and repository settings;
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

### Deterministic gates

- clean locked restore;
- build with zero errors and recorded warnings;
- full visible test suite;
- evaluator-owned hidden behavioral tests;
- `dotnet format --verify-no-changes`;
- vulnerable transitive package scan;
- publish and start the application;
- HTTP/browser smoke for success, validation, and divide-by-zero paths;
- no secrets, generated artifacts, or unrelated files in Git.

Hidden tests should cover decimal signs and scale, whitespace/invalid form
input, division by zero, overflow behavior, repeated submissions, and all four
operations without exposing those exact cases to either implementation agent.

### Blinded rubric

Score each arm from 0–3 on:

1. acceptance-criteria correctness;
2. edge-case behavior;
3. test quality and meaningful negative coverage;
4. maintainability and clarity;
5. idiomatic modern C#/.NET usage;
6. architecture proportionality and absence of needless abstraction;
7. security/input-handling posture;
8. accessibility and user feedback;
9. reproducible build/CI quality;
10. scope discipline.

The evaluator reports concrete file/line evidence before the arm identities are
revealed. Human review then records any defects the automated evaluator missed.

## Operational and cost measures

Record independently of the blinded quality score:

- wall-clock time from authorization to PR;
- Codex tokens/cost when available;
- number and severity of reviewer findings;
- number of fix/re-review cycles;
- tool failures, retries, and human interventions;
- PR readiness, diff size, test count, and changed-file count;
- plugin setup and maintenance time.

## Decision rule

The plugin advances to repeated trials when the treatment:

- violates no human gate or merge boundary;
- passes all deterministic gates;
- scores at least as high as control overall;
- shows a concrete quality gain in at least one correctness, edge-case, test, or
  maintainability dimension; and
- does not add more than 50% wall-clock/token cost without a material quality
  benefit.

After at least three matched pairs, adopt it for personal .NET work only if the
median treatment score improves by at least 10%, no safety regression occurs,
and operational overhead remains acceptable. Otherwise revise or reject the
plugin rather than moving the threshold after observing results.

## Execution sequence

1. Implement and test the bundled prompt-plugin loader in the Factory fork.
2. Build the `dotnet-quality` bundle at pinned third-party revisions.
3. Create the immutable calculator seed and evaluator-owned hidden harness.
4. Prepare two clean, equivalent Factory deployments from the same LXC template
   and provisioning inputs.
5. Validate an empty control plugin set and one treatment plugin set in worker
   health, prompt snapshots, logs, and manifests.
6. Randomize run order, record the assignment privately, and run both arms.
7. Freeze final commits, perform blinded evaluation, reveal assignment, and
   compare quality, cost, and safety.
8. Record gaps and decide whether to repeat, revise, or stop.

## Preconditions requiring Manav's decision

- whether the two calculator repositories may be public so GitHub's free branch
  protection can technically enforce PR-only changes;
- explicit approval of the final calculator specification before either
  implementation run.

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
