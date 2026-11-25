# Roadmap: v0.1.4 → v0.3

Next steps after v0.1.3. Each milestone is its own branch and release: tests, README, schemas, a tag and a tap bump. They're ordered so that each one builds on the last, and so that the quick correctness fixes ship first.

| Milestone | Release | Contents | Size |
|---|---|---|---|
| 1 | v0.1.4 ✓ | `options_cmd` errors shown; param names validated | small |
| 2 | v0.1.5 ✓ | `terminal: true`; panel PTY sized; run last | medium |
| 3 | v0.2.0 | `confirm:` popup; `lazycmd ask` / `lazycmd pick` | large |
| 4 | v0.2.1 | docker compose and Python features | medium |
| 5 | v0.3.0 | config reloads by itself; search in output | medium |

---

## Milestone 1 (v0.1.4): correctness ✓ done

### `options_cmd` failures are shown

Today a failing or slow `options_cmd` just leaves the list empty ([paramsform.go](internal/ui/paramsform.go) `runOptionsCmd` drops the error).

- `runOptionsCmd` returns `(OptionList, error)`. The error says what happened: `exit 1: <first line of stderr>`, `timed out after 5s`, or `printed nothing`. The text is capped at ~200 characters and cleaned with `safetext.Line`. The cache stores the error too, so it isn't re-run on every redraw.
- The field shows it in its description, in red: `⚠ options_cmd failed: exit 1: gcloud: not logged in`. It's wired through `DescriptionFunc` with the same bindings as the options, so it clears when a `depends_on` change makes the command succeed.
- An empty result from a command that worked shows `no options` instead of a blank list.
- A required select with no options can't be submitted, and the form says why.
- **Tests:** a failing command, a timeout (with the timeout cut short in tests), empty output, and the error clearing after a dependency changes.

### Param names are validated

- In `validateParams` ([args.go](internal/config/args.go)), names must match `^[A-Za-z_][A-Za-z0-9_]*$` and be unique within a command. Otherwise the command is skipped with a warning, the same as a bad pattern: `param "my name": names may only use letters, digits and _ (it becomes $LC_MY NAME, which no shell can read)`.
- This closes the `a=b` → `LC_A=B=value` gap.
- Feature commands' params are already valid. Profile `defaults:` / `params:` keys that don't match a param already warn.
- **Tests:** a space, `-`, `=`, a leading digit, a duplicate, and a valid name with digits and `_`.

---

## Milestone 2 (v0.1.5): terminal and run last ✓ done

### `terminal: true`

- A new spec field `terminal: bool` is allowed in commands.yaml, sidecars and includes. It's also settable from a profile per command, since a global script's sidecar can't know that you always want it in a terminal.
- With it set, `enter` runs the command the way `t` does: through the real terminal, with lazycmd coming back when it exits. A command with params opens its form first, then runs in the terminal.
- The row shows a dim `⧉` after the name, and the key bar says `enter run in terminal`.
- `t` still works for any command.

### Panel PTY gets a real size

- `jobs.Start` uses `pty.StartWithSize` with the Output panel's inner size. On a window resize, every running job gets `Setsize`, which sends it SIGWINCH. Today the size is 0×0, which confuses anything that formats to the width.
- Panel jobs get `LC_PANEL=1`, so a script can detect the panel and choose a simpler mode, or print "press t to run in a terminal".

### Run last

`enter` keeps opening the form with the **defaults**, as now. The new "run last" uses the answers you actually gave last time.

| Key | Where | Does |
|---|---|---|
| `.` | Commands | run the **last command you ran in this repo**, with the same answers (like a shell's `!!`) |
| `R` | Commands | run the **selected** command with the answers you last gave it in this repo |
| `r` | Jobs | restart a job from this session (unchanged) |

- **Storage:** `~/.local/state/lazycmd/last.json`, mode `0600`, keyed by repo and command id. It records the values and when they were used. Answers are saved when a run starts, from both the panel and the terminal.
- **Secrets are never stored.** If the command has a `secret` param, `R` / `.` open the form pre-filled with the stored answers and the secret left empty, rather than running.
- **Stale answers:** if a stored value no longer fits (an option is gone, a pattern fails, a param is new), lazycmd opens the form pre-filled instead of running, with a note saying why.
- **Same checks as a normal run:** shadow warnings, the trust prompt (a hidden command can't be re-run) and `confirm:` (Milestone 3) all apply.
- The key bar shows `. deploy (12m ago)` when there's a last command for this repo.
- **Tests:**
  - storage round trip and file permissions
  - secrets not stored, and the form opening instead
  - stale values opening the form
  - `.` with nothing stored
  - per-repo separation

---

## Milestone 3 (v0.2.0): confirm and ask

### `confirm:` popup

```yaml
deploy:
  script: ./scripts/deploy.sh
  confirm: "Deploy {services} to {env}?"   # or confirm: true for "Run deploy?"
```

- The popup shows **after** the form is submitted (or straight away for a command with no params) and **before** anything runs. That includes `R`, `.` and `r` restarts, which matter most here.
- `{name}` uses the same placeholder engine as `args:`. Values are cleaned for display, and secrets show as `••••••`.
- `y` runs. `n` / `esc` go back to the form with the answers kept. `enter` does **not** confirm, the same rule as the trust prompt, so a stray enter is safe.
- Yellow styling and layout match the shadow and trust prompts.
- A profile can add a confirm to any command (`confirm:` map keyed like `defaults:`), e.g. to guard `npm: deploy` in one repo.

### `lazycmd ask` / `lazycmd pick`

Lets a script ask a question mid-run through a lazycmd popup, with a plain-terminal fallback, so the same script works everywhere.

```bash
name=$(lazycmd ask "Release name?" --default "$(date +%F)")
token=$(lazycmd ask "Token?" --secret)
module=$(find . -name pom.xml | lazycmd pick "Module?")          # value<TAB>label lines work
targets=$(printf 'api\nweb\nworker\n' | lazycmd pick "Targets?" --multi)
```

- **Transport:** each session creates a Unix socket in a fresh `0700` folder under the state dir. Each panel job gets `LC_ASK_SOCK` plus a per-job random `LC_ASK_TOKEN`, so a request is tied to the job that made it, and the popup can name it.
- **Protocol:** one JSON line per request and one per answer. The request is `{token, kind, prompt, options, multi, secret, default}`. The answer is `{value}` or `{cancelled}`.
- **Exit codes:** `0` and the answer on stdout; `1` if cancelled, like fzf; `2` for a usage error.
- **UI:**
  - If the job is the one being shown, its popup opens right away. It's a small huh form, the same look as the params form, and `pick` is filterable.
  - Otherwise the job gets the `?` marker in the Jobs list (the existing "needs input" mark), and its popup opens when you select it.
  - Requests queue per job.
  - If the job exits, its pending request is dropped.
- **Outside lazycmd** (no `LC_ASK_SOCK`, including `t` / `terminal: true` runs):
  - `ask` reads from `/dev/tty`, with echo off for `--secret`.
  - `pick` runs `fzf` if it's installed, otherwise a numbered menu on `/dev/tty`.
- **Why a socket:** text a program prints can't open a popup or fake one. The guess-the-prompt popup stays as the fallback for programs that don't use `ask`.
- **Tests:**
  - protocol round trip, using a fake UI
  - tokens: wrong token rejected
  - cancel → exit 1
  - job exits with a pending request
  - each fallback path, using a fake tty
  - multi pick
  - `value<TAB>label` input

---

## Milestone 4 (v0.2.1): features

Both follow the existing rules: detection and listing only **read files**, and nothing from the repo runs until you pick a command. Both can be switched off with `disable:` and both have settings. The schema enums gain `compose` and `python`.

### docker compose (`compose`)

- **Detected by:** `compose.yaml`, `compose.yml`, `docker-compose.yaml` or `docker-compose.yml` in the repo root.
- **Read:** service names, and each service's `profiles:`.
- **Commands** (all `compose: …`):

  | Command | Params |
  |---|---|
  | `up` | `services` multi, `detach` bool (default true), `build` bool, `profile` select (if any) |
  | `down` | `volumes` bool |
  | `ps` | |
  | `logs` | `service` select (optional), `follow` bool |
  | `build` | `services` multi, `no_cache` bool |
  | `restart` | `services` multi |
  | `pull` | `services` multi |
  | `exec` | `service` select, `command` string (default `sh`). **`terminal: true`**, since it's interactive |

- **Settings:** `compose.command` selects the CLI: `docker compose` (default), `docker-compose` or `podman compose`.
- Arguments are built the same way as Maven's: one flag per line of shell from `LC_` vars, values quoted, and a preview.

### Python (`python`)

- **Detected by:** `pyproject.toml`. It needs a TOML parser, a new dependency: `pelletier/go-toml/v2` or `BurntSushi/toml`.
- **Runner from lockfile:** `uv.lock` → `uv run`, `poetry.lock` → `poetry run`, `pdm.lock` → `pdm run`, otherwise plain (`python -m …`). Overridable with `python.runner`.
- **Commands:**
  - one per entry in `[project.scripts]` / `[tool.poetry.scripts]`
  - `[tool.pdm.scripts]` and `[tool.poe.tasks]` (poethepoet) task names
  - `pytest` when `[tool.pytest.ini_options]` exists or pytest is a dependency, with `filter` (`-k`) and `path` params
  - `ruff check` / `ruff format` when `[tool.ruff]` exists
  - `mypy` when `[tool.mypy]` exists
- Names from the file are quoted the way npm script names are, and names starting with `-` are skipped.

---

## Milestone 5 (v0.3.0): reload and search

### Config reloads by itself

- **What's watched:**
  - global `config.yaml`, `scripts/` and `projects/`
  - the repo's `.lazycmd/`
  - the files features read: `pom.xml` and module poms, `package.json`, compose files, `pyproject.toml`
- **How:** check mtimes and folder listings every second, debounced. This needs no new dependency and handles editors that save by renaming. fsnotify is the alternative if checking proves too slow.
- **On change:** the command list is rebuilt through the same `build()` as at startup:
  - the selection is kept by command id, and the filter is kept
  - running jobs and open forms are untouched; an open form for a changed command keeps its old spec until it closes
  - a line in the Output start text says what reloaded and lists any new warnings
- **Trust:** if a repo's commands change, the fingerprint changes. They're hidden again and the yellow "review" row reappears, the same as on startup.

### Search in output

- `/` in the Output panel opens a search line. Matches are highlighted, `n` / `N` go to the next / previous match, and `esc` clears.
- It searches the job's buffer with ANSI codes removed. It's case-insensitive unless the query has capitals ("smart case").
- Searching stops following, the same as scrolling up does.
- It works on running and finished jobs, and shows a match count (`3/17`) in the panel title.

---

## Decisions

1. **Keys for run last:** `.` (last command in this repo) and `R` (selected command, last answers).
2. **`confirm:` in profiles:** yes. A profile can add a confirm to any command, but can't remove one a command sets itself.
3. **`ask` popups for a job you're not viewing:** mark the job with `?` and wait. Its popup opens when you select it, and it never takes focus away from what you're doing.
4. **TOML library** for Python: `pelletier/go-toml/v2` (maintained, strict decoding, no dependencies).
5. **Reload:** check mtimes and folder listings once a second. No fsnotify.
