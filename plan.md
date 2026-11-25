# Lazycmd: plan

A lazygit-style TUI for running scripts. It has global and per-project commands, context-aware menus (pom, package.json, Vite config), huh forms for parameters, streaming output in a large panel, background jobs, and interactive prompts handled inside the app.

Design mock (main view, parameter form, prompt popup): https://claude.ai/artifact/VmyRMBGae9bZpr85j9jSnX

## Decisions so far

- **Stack:** Go with Bubble Tea, bubbles, lipgloss and huh. Also `creack/pty` for running commands and `charmbracelet/x/ansi` for ANSI-aware wrapping. Chosen over Ink for startup time and a single static binary.
- **Config is YAML**, decoded strictly so typos are errors, with a JSON Schema published for editor validation and autocomplete.
- **Plain scripts work with zero config.** A script can be "upgraded" by a sidecar `<script>.lazycmd.yaml` file next to it that describes its form.
- **Projects have YAML** in `.lazycmd/`.
- **Features (maven, npm, vite, git) are opt-in providers** that emit the same command spec as the YAML files. There is one code path from spec to huh form, whether the spec came from a project file, a sidecar or a feature.
- **Output goes through a PTY, not pipes**, so tools keep colors and progress bars and prompts reach us.
- **Interactive programs:** the long-term answer is a terminal emulator in the output panel. Simple prompts get a popup first. Detecting zellij or tmux and opening a pane was considered and dropped.
- **Parameters reach scripts as env vars** (`LC_<NAME>`), never by substituting text into a command line.

## Command list layout

Top to bottom, as in the mock:

1. **Global**: all global scripts in one group, with the ones used most in the current project first, then the rest.
2. **This project**: scripts and commands from `.lazycmd/`.
3. **Discovered**: one group per enabled feature (Maven, NPM, Vite, git), each labelled with its source file.

Rules:
- A project command shadows a global command with the same name.
- Feature commands are namespaced (`maven: test`, `npm: lint`) so they never collide with scripts.
- "Used most here" comes from a small usage log, keyed by repo root and a stable command id (`global:deploy-staging`, `maven:test`). It lives in `~/.local/state/lazycmd/` and uses a decayed count, so recent runs weigh more and old favourites don't stick.
- `/` filters across all groups.

## Files and layout

```
~/.config/lazycmd/
  config.yaml              # global settings, enabled features
  scripts/
    clean-docker.sh        # plain script: works with no config
    deploy-staging.sh
    deploy-staging.lazycmd.yaml   # sidecar: upgrades the script with a form

~/.config/lazycmd/projects/
  vandag.yaml              # central per-repo profile (see "Project profiles")

<repo>/.lazycmd/
  config.yaml              # features for this repo, feature settings
  commands.yaml            # project command specs
  scripts/
    cloud-sql-proxy.sh
    cloud-sql-proxy.lazycmd.yaml
```

- A plain executable in a `scripts/` dir becomes a command named after the file, with no params.
- A sidecar `<script>.lazycmd.yaml` adds description, params and mode to that script. The `.lazycmd.yaml` suffix keeps it from colliding with unrelated YAML files.
- `commands.yaml` holds commands that are inline (`run:`) or that point at a script (`script:`) and carry their own spec.
## Command spec

One schema, used by project files, sidecars and features:

```yaml
# .lazycmd/commands.yaml
commands:
  cloud-sql-proxy:
    desc: Start the Cloud SQL proxy
    mode: background          # panel (default) | background
    params:
      - name: instance
        type: select
        options_cmd: gcloud sql instances list --format='value(connectionName)'
      - name: port
        type: string
        default: "5432"
        pattern: '^[0-9]+$'
    run: |
      exec cloud-sql-proxy --port "$LC_PORT" "$LC_INSTANCE"

  deploy:
    desc: Deploy a service
    params:
      - { name: env, type: select, options: [dev, staging, prod] }
      - { name: service, type: select, options_cmd: ls services, depends_on: [env] }
      - { name: dry_run, type: bool, default: true }
    script: ./scripts/deploy.sh
```

A sidecar is the same spec for a single script, without the `commands:` wrapper:

```yaml
# deploy-staging.lazycmd.yaml  (next to deploy-staging.sh)
desc: Deploy to staging
params:
  - { name: service, type: select, options_cmd: ls services }
```

### Passing params to a script

- **Env vars** (always): every param is also `LC_<NAME>`.
- **`args:`** (scripts only): a list where each entry is exactly one argument and `{param}` is replaced by the value, e.g. `args: ["--env", "{env}", "--tag={tag}"]`. No shell is involved, so a value like `a b; rm -rf ~` stays one harmless argument. An entry whose placeholders are all empty is dropped (optional flags). `{{`/`}}` are literal braces. `args:` with `run:` is rejected; inside a `run:` body use `"$LC_NAME"`, never text substitution.

### Params and the huh form

| Param type | huh field |
|---|---|
| `string` | `Input`, with `Validate` from `pattern` / `required` |
| `select` | `Select` (static `options` or `options_cmd`) |
| `multi` | `MultiSelect` |
| `bool` | `Confirm` |
| `secret` | `Input` with `EchoModePassword` |
| `text` | `Text` |

- Build the form at runtime: one bound value per param, fields collected into a `huh.NewGroup`, results read from a map.
- Embed the form as a `tea.Model` (`Init`/`Update`, check `form.State`). Do not call `form.Run()`, which starts its own program.
- `depends_on` maps to `OptionsFunc`, which re-evaluates when the bound field changes. Check how huh behaves while a slow `options_cmd` is running.
- `options_cmd` runs in the project root with a timeout and a cache.
- The popup shows a live preview of the final command, rendered from the bound values.
- Override huh's keymap so Esc cancels the popup.

### Safety

- Opening lazycmd in a repo executes nothing from that repo's config. Features only parse files (pom, package.json) and never invoke `mvn` or `npm` to discover commands.
- `options_cmd` and scripts run only after the user selects a command.
- Params go in as env vars to avoid quoting problems and injection from unusual values.

## Running commands

- Each run is a PTY-backed job with its own buffer, status, duration and exit code. Buffers live outside the Bubble Tea model.
- Output panel: `bubbles/viewport`, follow-tail by default, stop following when the user scrolls up, capped scrollback (around 10k lines), `/` search, `y` copy.
- Every run is also teed to a log file.
- Jobs list shows running, background, needs-input, succeeded and failed jobs. Background jobs (proxy, dev server) stay alive while you run other commands, with stop and restart keys.
- Interactive input:
  1. **Focus mode.** Forward keys to the PTY (runes as-is, Enter as `\r`, arrows as escape sequences, Ctrl-C as `\x03`). A reserved key leaves the mode.
  2. **Prompt detection.** Output pauses without a trailing newline for ~300 ms and the tail looks like a prompt (`[y/N]`, `?`, `:`). Open a popup with a `textinput`. Check the PTY's `ECHO` flag via `x/sys/unix` to mask passwords. Background jobs get a "needs input" badge instead of stealing focus.
  3. **Embedded terminal emulator** for full-screen programs (`vim`, `less`, `psql`). Evaluate Charm's `x/vt` and community components before committing. Keep `tea.ExecProcess` (suspend and resume) as the fallback.

## Project profiles

`~/.config/lazycmd/projects/<name>.yaml` is a per-repo profile kept outside the repo, so nothing has to be committed to a project:

```yaml
path: ~/repos/vandag            # or paths: [...]; with no path, the file name must equal the repo folder name
include:                        # extra commands for "This project"
  - ~/shared/db/migrate.lazycmd.yaml   # sidecar (script beside it)
  - ~/shared/tools/commands.yaml       # commands.yaml-shaped file
  - ~/shared/scripts                   # folder of scripts
defaults:                       # preselected form values, for any command (global, project, feature)
  deploy-staging: { env: staging }
  "maven: test": { module: api, also_make: true }
  deploy: { env: staging, service: "${env}-api" }   # ${param} refers to another param's default
disable: [git]                  # plus features:/maven:/... like config.yaml
```

- Matched by path (the repo or any folder below a listed path); first match in file-name order wins. Invalid files are warnings, never fatal.
- `defaults` only preselect; the user can still change them. `${param}` references resolve once, when the form opens, from the other param's default (they chain; cycles and unknown names are warnings). They don't follow what the user later types.
- Unknown commands/params and select values outside the options are warnings.
- Layering for feature settings: global `config.yaml`, then the profile, then the repo's own `.lazycmd/config.yaml`.
- The repo's own `.lazycmd/` commands win over an included command with the same name.

## Features

A feature is a provider with a detect step and a commands step, and it emits command specs in the schema above:

```go
type Feature interface {
    Name() string
    Detect(dir string) bool
    Commands(dir string) ([]CommandSpec, error)
}
```

| Feature | Detects | Emits |
|---|---|---|
| maven | `pom.xml` | lifecycle phases; module and profile params from the pom (`encoding/xml`, cached by mtime); declared linters (spotless, checkstyle, spotbugs, pmd) |
| npm | `package.json` | scripts, workspaces, package manager chosen from the lockfile |
| vite | `vite.config.*` | `dev` and `preview` (background), `build` |
| git | `.git` | fetch and prune, stashes, worktrees, actions on the current branch |

- Features are on by default and show only when their files are detected (parsing only, nothing executes). `config.yaml` can turn some off (`disable: [git]`), re-enable them (`features: [git]`) and set feature settings such as `maven.default_profile`. Global is applied first, then the project.
- Later, optional: external features as executables that print specs for a `list` call, so they can be written in any language.

## Build order

0. **Spike:** main view with a fake output stream (lipgloss layout, viewport, key bar), per the mock.
1. **Discovery:** load global and project scripts, sidecars and `commands.yaml`; strict YAML decoding; grouped, filterable command list.
2. **Run and stream:** PTY job, per-job buffer, coalesced redraws, jobs list, follow and scroll, log tee.
3. **Params form:** spec to huh form, env var passing, `options_cmd` with cache, `depends_on`, command preview.
4. **Usage ordering:** usage log and "used most here first".
5. **Features:** provider interface, then Maven (it exercises detection, param sources and caching), then npm, Vite, git.
6. **Input:** focus mode, then prompt detection and popup.
7. **Terminal emulator** panel, if real use shows it's needed.
8. **Polish:** `?` help overlay, JSON Schema, packaging, name and binary alias.

## Gotchas to remember

- Never mutate the model from a goroutine. Read the PTY in a goroutine and deliver chunks as messages.
- Coalesce output so a noisy build doesn't flood `Update` (redraw at most every ~16–50 ms).
- Handle `\r` in the line buffer so progress bars don't fill the panel with duplicate lines.
- Use ANSI-aware wrapping (`x/ansi`), not plain string slicing, so color sequences aren't split.
- Bubble Tea v1 has no overlays. Splice popups over the base view yourself, or replace the output panel region while a popup is open.
- Titles cut into panel borders (`[1] Commands`) need a hand-built top border line.
- Keep bubbletea, bubbles, lipgloss and huh on the same major version line, and check whether v2 is the current stable line before starting.
- `creack/pty` is Unix-only. Windows would need ConPTY.

## Open questions
- Whether to keep `mode: background` as an explicit field or infer it from long-running commands.
- Format for the usage log (JSON file vs SQLite).
- ~~Name~~ Resolved: renamed from "lazycast" (clashes with an existing GitHub project) to **lazycmd**, with `lc` as the short alias. Still worth checking Homebrew and the GitHub org/module path before publishing.
