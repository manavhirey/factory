# Factory Starter Guide

This guide takes you from a fresh checkout to your first completed piece of
Work. You will run the control plane and one Worker on the same computer, add a
GitHub repository, save a Routine, and inspect its result in the browser.

Factory is in developer preview. Start with a repository you trust and can
recover, and do not expose the browser UI beyond the local machine.

## The four things to know

- A **Repository** is a GitHub repository that Factory can make available to
  Workers.
- A **Worker** is a computer that has Git, repository access, and at least one
  supported coding-agent runtime.
- A **Routine** is a saved prompt plus its runtime, repositories, timeout, and
  optional schedule.
- **Work** is one run of a Routine. Factory creates one Target for each selected
  repository and records its attempts, events, and result.

## 1. Check the prerequisites

The local starter setup supports macOS and Linux. Install:

- Go 1.25.13 or newer on the 1.25 release line, or Go 1.26.5 or newer;
- Git, `curl`, and [`just`](https://just.systems/);
- the [GitHub CLI](https://cli.github.com/); and
- at least one supported coding-agent CLI: Pi, Codex, or Claude Code.

Confirm that the GitHub CLI can access the repository you want to use:

```sh
gh --version
gh auth status
```

Run your chosen coding-agent CLI once as well. It must be installed and
authenticated for the same operating-system user that will run the Worker.

## 2. Build Factory

Clone this repository, build the control plane and Worker, and copy the example
Worker configuration:

```sh
git clone https://github.com/manavhirey/factory.git
cd factory
just build
mkdir -p ~/.factory
cp examples/worker.toml ~/.factory/worker.toml
```

If you already have a Factory checkout, run `git pull --rebase` in it and begin
with `just build`.

## 3. Configure one local Worker

Open `~/.factory/worker.toml` and advertise only the runtime you want to use for
this first run. For example, a Codex Worker can start with:

```toml
server = "http://127.0.0.1:7337"
name = "local"
runtime = "codex"
runtimes = ["codex"]
max_concurrent = 1
```

Use `pi` or `claude-code` in both runtime fields if that is the CLI you have
installed. A concurrency limit of one keeps the first run easy to observe; you
can raise it later.

## 4. Start Factory

From the repository root, run:

```sh
just run
```

This starts the control plane and local Worker together. Leave the terminal
open, then visit [http://127.0.0.1:7337](http://127.0.0.1:7337).

Select **Workers** in the navigation. The `local` Worker should be online and
healthy, and your chosen runtime should be ready. If a runtime is shown as
missing or unauthenticated, fix that CLI on the Worker host and restart
`just run`.

## 5. Add a repository

1. Select **Repositories**.
2. In **Add GitHub repository**, enter its canonical identity, such as
   `github.com/your-name/your-repository`.
3. Select **Add repository**.
4. Wait for the repository to become ready.

Factory centrally manages the repository identity. The Worker uses its local
`gh` credentials to acquire the repository when Work is assigned.

## 6. Create a first Routine

Select **New Routine** and use these beginner-safe settings:

- **Name:** `README review`
- **Prompt:**

  ```text
  Review README.md for broken internal links and unclear setup instructions.
  Do not edit any files. Return a concise list of findings with file and line
  references, and say explicitly when no issues are found.
  ```

- **Runtime:** the runtime advertised by your Worker
- **Timeout:** `30m`
- **Parallel targets:** `1`
- **Repositories:** select the repository you just added
- **Schedule:** leave disabled for the first run

Select **Save Routine**.

## 7. Run and inspect the Work

On the Routine, select **Run now**, then open **Work**. The Work detail page
shows the Target for your repository, its current state, attempt events, and
the final result.

A successful read-only review should finish without repository changes. For a
later Routine that edits files, Factory runs the agent in an isolated branch
and worktree. It does not merge changes into your default branch. Review the
result and use your normal Git review process before accepting any change.

Press `Ctrl+C` in the terminal when you are finished. This stops both local
processes; Factory keeps its state under `~/.factory` for the next start.

## Common first-run problems

| Symptom | What to check |
| --- | --- |
| `just run` cannot find Worker configuration | Copy `examples/worker.toml` to `~/.factory/worker.toml`. |
| Worker is offline or unhealthy | Check the `just run` terminal, then verify Git and the selected runtime CLI are available to the same user. |
| Runtime is missing or unauthenticated | Run that agent CLI directly, complete its authentication flow, and restart Factory. |
| Repository does not become ready | Run `gh auth status --hostname github.com` and confirm that account can access the repository. |
| Work remains blocked | Compare the Routine runtime with the ready capabilities on **Workers**, and inspect the repository status for an access error. |
| Port 7337 is already in use | Start with `FACTORY_LISTEN=127.0.0.1:7444 just run`, then open `http://127.0.0.1:7444`. |
| Startup reports old preview data | Do not delete the database. Follow the backup or recovery instruction printed by the launcher. |

For deeper troubleshooting and separate server/Worker commands, read the
[local guide](local.md). The [worker guide](worker.md) explains runtime health,
capacity, branches, and worktree cleanup. When you are ready to automate a
Routine or add more repositories, continue with [Routines and Work](routines/design.md).

To run capacity on another machine, use the [remote VM Worker guide](remote-workers.md).
Plugins are optional, operator-enabled extensions; establish a successful basic
run before adding them.
