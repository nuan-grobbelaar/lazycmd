# lazycmd

A lazygit-style terminal UI for running your scripts and project commands. Pick a command, fill in its parameters in a form, and watch it run as a job. Alias: `lc`.

```bash
make install     # installs lazycmd and the lc alias to GOBIN (or GOPATH/bin)
cd ~/some/repo && lc
```

Opening lazycmd in a repo never executes anything from that repo's config. Commands only run after you select them, and a repo's own commands are only listed once you [allow them](#allowing-a-repos-commands). Text from the repo (command names, descriptions, the branch name in `.git/HEAD`, script names in `package.json`) has control characters made visible before it is drawn (ESC shows as `^[`), so a repo can't send escape sequences to your terminal.

- [Screen and keys](#screen-and-keys)
- [Where commands come from](#where-commands-come-from)
- [Allowing a repo's commands](#allowing-a-repos-commands)
- [Files and folders](#files-and-folders)
- [Command spec](#command-spec)
- [Params](#params)
- [Passing params to a command](#passing-params-to-a-command)
- [Plain scripts and sidecars](#plain-scripts-and-sidecars)
- [commands.yaml](#commandsyaml)
- [config.yaml: features and settings](#configyaml-features-and-settings)
- [Features](#features)
- [Project profiles](#project-profiles)
- [State: logs and usage](#state-logs-and-usage)
- [Editor support: JSON Schemas](#editor-support-json-schemas)

## Screen and keys

The left column has `[1] Commands` over `[2] Jobs`. `[3] Output` fills the right. The header shows the folder, git branch, matched profile and detected features.

| Key | Where | Does |
|---|---|---|
| `↑`/`↓`, `j`/`k` | Commands, Jobs | move the selection |
| `enter` | Commands | run the selected command (opens its form first if it has params) |
| `t` | Commands | run it with the real terminal, for full-screen programs (vim, less, psql) |
| `/` | Commands | filter by name (`enter` keeps it, `esc` clears it) |
| `enter` | Jobs | show that job in the Output panel |
| `x` | Jobs | stop the job |
| `r` | Jobs | restart it with the same params |
| `i` | Output | focus mode: keys go straight to the running job; `esc` leaves |
| `↑`/`↓`, `pgup`/`pgdn` | Output | scroll (scrolling up stops following; reaching the bottom resumes) |
| `tab` | anywhere | next panel |
| `←`/`→` | anywhere | previous / next panel |
| `alt+1` / `alt+2` / `alt+3` | anywhere | jump to Commands / Jobs / Output |
| `?` | anywhere | help |
| `q`, `ctrl+c` | anywhere | quit (kills jobs still running) |

On macOS, option+1/2/3 types `¡ ™ £` unless your terminal treats Option as Meta; both work.

In a params form: `tab`/`shift+tab` move between fields, `enter` confirms a field and submits on the last one, `esc` cancels.

**Run now:** `alt+enter` (or `ctrl+j`) runs the command straight away with the answers so far, without stepping through the remaining fields. It is refused, with the reason shown in the form, while a required field is empty or a value doesn't match its `pattern`; the `alt+enter run now` hint in the bottom bar appears only when the form is ready to run. Selects and bools use whatever they currently show (their default or first option).

Terminals send `shift+enter` as a plain `enter`, which a terminal UI cannot tell apart, so it can't be used directly. If you want it anyway, make your terminal send a newline for it: in Ghostty `keybind = shift+enter=text:\n`, in iTerm2 a profile key mapping of Shift+Return to "Send Hex Code" `0x0A`. lazycmd then sees `ctrl+j`, which also runs the form.

When a job stops at a prompt (`[y/N]`, `Password:`, …) a popup appears for the active job and takes your answer (`enter` sends, `esc` dismisses; typing is masked when the program turned echo off). A job that isn't the one shown in the Output panel shows `needs input` in the Jobs panel instead of taking focus; select it there to answer.

Output keeps colours and styles but drops screen-control sequences (clear screen, cursor movement, titles), so programs like `vite dev` can't disturb the UI. Tabs are expanded to 8 columns. Scrollback is capped at 10 000 lines.

## Where commands come from

The command list is grouped, top to bottom:

1. **Ad-hoc**: `run command…`, which asks for any shell command line and runs it as a job in the current folder.
2. **Global**: scripts in `~/.config/lazycmd/scripts/`, most-used-in-this-repo first.
3. **This project**: scripts and commands from the repo's `.lazycmd/`, plus anything a [profile](#project-profiles) includes.
4. **Discovered**: one group per detected [feature](#features) (Maven, NPM, Vite, git), labelled with its source file.

Rules:
- A project command **shadows** a global command with the same name. Such a command is shown in **yellow**, and the first time you run it in a repo lazycmd stops and shows which of your scripts it hides and what it will run instead (`y` runs it, anything else cancels). This happens before its form opens, so nothing from the project runs until you say yes. After the first run in that repo it isn't asked again. Deleting `usage.json` brings the question back.
- Feature commands are namespaced (`maven: test`, `npm: lint`) so they never collide with scripts.
- "Most used here" comes from a usage log keyed by repo and command, with a 14-day half-life, so recent runs weigh more.
- Files that fail to parse are skipped and reported as warnings in the Output panel's start text. They never stop lazycmd from starting.

## Allowing a repo's commands

A repo's `.lazycmd/` decides what its commands are called and what they run, so the first time you open a repo that defines commands, lazycmd asks before listing them. The prompt lists each command with what it runs. `y` allows them; `n`, `esc` or `enter` leaves them hidden for now, and a yellow `N not allowed: review…` row in their place reopens the prompt. Everything else (your global scripts, profile includes, discovered features, ad-hoc) works either way.

The answer is remembered per repo with a fingerprint of `commands.yaml`, every file in `.lazycmd/scripts/`, and every script a command points at (even outside `.lazycmd/`). If any of that changes, for example after a `git pull`, lazycmd asks again and says the commands changed. The repo's `config.yaml` is not covered: it only switches features and settings, and runs nothing.

Allowed repos are kept in `~/.local/state/lazycmd/trust.json` (readable only by you). Delete an entry, or the file, to be asked again.

## Files and folders

```
~/.config/lazycmd/
  config.yaml                       # global feature settings
  scripts/
    clean-docker.sh                 # plain executable: a command with no params
    deploy-staging.sh
    deploy-staging.lazycmd.yaml     # sidecar: gives the script a form
  projects/
    vandag.yaml                     # central per-repo profile

<repo>/.lazycmd/
  config.yaml                       # feature settings for this repo
  commands.yaml                     # command specs
  scripts/
    cloud-sql-proxy.sh
    cloud-sql-proxy.lazycmd.yaml

~/.local/state/lazycmd/
  usage.json                        # usage counts
  trust.json                        # repos whose commands you allowed
  logs/                             # one log file per run
```

An executable file directly inside a `scripts/` folder becomes a command named after the file without its extension (`deploy-staging.sh` → `deploy-staging`). Files that aren't executable are ignored.

## Command spec

One shape is used everywhere: sidecars, `commands.yaml` entries and files a profile includes.

| Key | Type | Meaning |
|---|---|---|
| `desc` | string | description, shown at the top of the params form |
| `params` | list | the form's fields, see [Params](#params) |
| `run` | string | inline shell body, run with `sh -c` |
| `script` | string | path to a script (relative to the file that names it) |
| `args` | list of strings | arguments for the script, see [below](#passing-params-to-a-command) |

In `commands.yaml` an entry must set exactly one of `run` or `script`. In a sidecar the script is the file next to it.

```yaml
desc: Deploy a service
params:
  - { name: env, type: select, options: [dev, staging, prod] }
  - { name: service, type: select, options_cmd: ls services, depends_on: [env] }
  - { name: dry_run, type: bool, default: true }
```

Unknown keys are errors, so a typo is loud instead of silently ignored.

## Params

| Key | Meaning |
|---|---|
| `name` | required; the param's name (see [passing](#passing-params-to-a-command)) |
| `type` | `string` (default), `select`, `multi`, `bool`, `secret` or `text` |
| `default` | initial value. Bools take `true`/`false`. For `multi`, a comma-separated list of options to preselect |
| `pattern` | regexp a `string` value must match |
| `required` | `string` only: the form refuses an empty value |
| `options` | static choices for `select` / `multi` |
| `options_cmd` | shell command whose output lines become the choices |
| `depends_on` | names of earlier params; when one changes, `options_cmd` runs again |

| Type | Form field |
|---|---|
| `string` | single-line input (with `pattern` / `required` validation) |
| `select` | pick one |
| `multi` | pick several (values arrive comma-joined) |
| `bool` | yes/no (arrives as `true` or `false`) |
| `secret` | input with masked echo; reaches the command only as `LC_NAME`, never in `args` |
| `text` | multi-line input |

`options_cmd` runs in the repo root with a 5 second timeout, with every param answered so far available as `LC_<NAME>`. Results are cached per command and answers, so a dependent select re-runs only when what it depends on changes. It only runs after you select the command, never on startup. Declare a param after the ones it `depends_on`.

The form shows a live preview of the final command line under the fields.

## Passing params to a command

Every param reaches the command in two ways:

**1. Environment variables, always.** A param `name` becomes `LC_NAME` (upper-cased). Values are never pasted into a command line, so quoting and injection problems don't arise. In a `run:` body, use them quoted:

```yaml
run: |
  exec cloud-sql-proxy --port "$LC_PORT" "$LC_INSTANCE"
```

**2. Arguments, with `args:` (scripts only).** For tools that read `$1` or `--flags` and know nothing about lazycmd:

```yaml
# deploy.lazycmd.yaml, next to deploy.sh
params:
  - { name: env, type: select, options: [dev, prod] }
  - { name: tag, type: string }
args: ["--env", "{env}", "--tag={tag}"]
```

- Each entry is exactly **one** argument. A value like `my api; rm -rf ~` stays one harmless argument; no shell is involved.
- `{name}` is replaced by that param's value. `{{` and `}}` are literal braces.
- An entry that uses placeholders **and** has all of them empty is dropped. That makes optional flags: `--tag={tag}` disappears when `tag` is empty. (Put a flag and its value in one entry for this, `--tag={tag}`, not two.)
- Bools are `true`/`false`; `multi` values are comma-joined.
- Only declared params may be used, and `args` needs a script. With `run:` it is an error. Use `"$LC_NAME"` in the shell body instead.
- A `secret` param can't be used in `args`; the command is skipped with a warning. A program's arguments can be read by any user on the machine (`ps`), while its environment can only be read by you, so a script gets its secrets from `$LC_NAME`.

## Plain scripts and sidecars

A script with no sidecar is a command with no params. Add `<script-name>.lazycmd.yaml` beside it to give it a description, a form or arguments. The sidecar is a [command spec](#command-spec) without the `commands:` wrapper:

```yaml
# deploy-staging.lazycmd.yaml
desc: Deploy to staging
params:
  - { name: service, type: select, options_cmd: ls services }
```

A broken sidecar produces a warning and that script is skipped. The `.lazycmd.yaml` suffix keeps sidecars from colliding with other YAML files.

## commands.yaml

`<repo>/.lazycmd/commands.yaml` holds commands that are inline (`run:`) or point at a script (`script:`, relative to the file):

```yaml
commands:
  cloud-sql-proxy:
    desc: Start the Cloud SQL proxy
    params:
      - name: instance
        type: select
        options_cmd: gcloud sql instances list --format='value(connectionName)'
      - { name: port, type: string, default: "5432", pattern: '^[0-9]+$' }
    run: |
      exec cloud-sql-proxy --port "$LC_PORT" "$LC_INSTANCE"

  deploy:
    script: ./scripts/deploy.sh
    params:
      - { name: env, type: select, options: [dev, staging, prod] }
    args: ["{env}"]
```

An entry that breaks the rules is skipped with a warning; the rest of the file still loads.

## config.yaml: features and settings

`~/.config/lazycmd/config.yaml` (global) and `<repo>/.lazycmd/config.yaml` (per repo). Both optional.

| Key | Meaning |
|---|---|
| `disable` | list of features to turn off: `maven`, `npm`, `vite`, `git` |
| `features` | list of features to switch back on after a broader file disabled them |
| `maven`, `npm`, `vite`, `git` | per-feature settings (string values) |

All features are **on by default** and only show when their files are detected, so there is nothing to enable. Order of application: global file, then a matching [profile](#project-profiles), then the repo's own file. Settings override key by key; `disable` / `features` apply in that same order.

```yaml
disable: [git]
maven:
  default_profile: local
```

Settings in use: `maven.default_profile` preselects the profile param on Maven commands. The `npm`, `vite` and `git` blocks are accepted but currently have no settings.

## Features

Features only **read files** (never run `mvn`, `npm`, `git` to find out what exists). Opening a repo executes nothing.

| Feature | Detected by | Commands |
|---|---|---|
| `maven` | `pom.xml` | the lifecycle phases (`validate`, `compile`, `test`, `package`, `verify`, `install`, `deploy`); `spotless:check`, `checkstyle:check`, `spotbugs:check`, `pmd:check` when the root pom declares those plugins; and `spring-boot:run`, `quarkus:dev`, `jetty:run` when the root pom **or any module pom** declares that plugin |
| `npm` | `package.json` | one command per script. The runner comes from the lockfile: `pnpm-lock.yaml` → pnpm, `yarn.lock` → yarn, `bun.lockb` → bun, otherwise npm |
| `vite` | `vite.config.*` | `dev`, `preview`, `build` |
| `git` | `.git` | `fetch --prune`, `pull`, `push`, `stash`, `stash pop`, `worktree list` |

Params added from your files:
- **Maven** with `<modules>`: a `module` select (runs `-pl`) and an `also_make` checkbox (`-am`, only with a module). With `<profiles>`: a `profile` select (`-P`).
- **Maven run goals** (`spring-boot:run`, `quarkus:dev`, `jetty:run`): module poms are read too, following `<modules>` down a few levels (only paths inside the repo). In a multi-module project a `module` select is required and lists only the modules that declare the plugin (every module when the root does, since children inherit it). There is no `also_make`, since `-am` would try to start the upstream modules too, so run `maven: install` first if the module depends on others. `spring-boot:run` adds a `spring_profiles` field (`-Dspring-boot.run.profiles=`) and `quarkus:dev` a `quarkus_profile` field (`-Dquarkus.profile=`). These are the framework's profiles, separate from Maven's `-P`. A plugin listed only under `<pluginManagement>` doesn't count.
- **npm** with `workspaces` (array form, or `{packages: [...]}`): every script found in the root or any workspace is listed, and a `workspace` picker chooses where to run it (`.` is the root). It uses the manager's own form: `npm run X --workspace NAME`, `pnpm --filter NAME run X`, `yarn workspace NAME run X`, `bun run --filter NAME X`.

## Project profiles

`~/.config/lazycmd/projects/<name>.yaml` is a per-repo profile kept outside the repo, so nothing has to be committed to a project. It adds commands, sets form defaults and tunes features.

```yaml
path: ~/repos/vandag            # or paths: [...]
include:
  - ~/shared/db/migrate.lazycmd.yaml    # a sidecar (its script is beside it)
  - ~/shared/tools/commands.yaml        # a commands.yaml-shaped file
  - ~/shared/scripts                    # a folder of scripts
  - ./deploy.sh                         # a script (+ sidecar beside it)
defaults:
  deploy-staging: { env: staging }
  "maven: test": { module: api, also_make: true }
  deploy:
    env: staging
    service: "${env}-api"
disable: [git]
maven:
  default_profile: local
```

| Key | Meaning |
|---|---|
| `path` / `paths` | the repo(s) this profile is for. `~` is expanded; a relative path is relative to the profile file. Matches the repo itself **or any folder below it** |
| `include` | extra commands for "This project" |
| `defaults` | preselected form values |
| `features`, `disable`, `maven`, … | exactly as in [config.yaml](#configyaml-features-and-settings) |

**Matching.** With no `path`, the file name (without `.yaml`) must equal the repo folder's name, so `vandag.yaml` matches `~/repos/vandag`. If several profiles match, the first by file name wins. The header shows `profile <name>` when one matched.

**include.** Each entry is one of: a script (its `<name>.lazycmd.yaml` beside it is picked up); a `<name>.lazycmd.yaml` sidecar (its script sits beside it, or the sidecar sets `run:` / `script:`); a `commands.yaml`-shaped file (any other `.yaml`/`.yml`); or a folder of scripts. A missing or invalid entry is a warning and the rest still load. The repo's own `.lazycmd/` wins over an included command with the same name, and an included command shadows a global one.

**defaults.** Keys are a command's display name (`maven: test`) or its id (`global:deploy-staging`, `maven:test`, `project:deploy`), then param names. This works for any command: global, project, included, or a discovered feature command. The values only preselect; you can still change them in the form.
- Values: strings, numbers, bools, and a list for a `multi` param.
- A `select` default must be one of its static options (otherwise it is ignored with a warning).
- `${param}` refers to another param of the same command and is replaced when the form opens, using that param's default. References chain; a cycle or unknown name is a warning and the text is left as written. They do **not** follow what you type into the other field afterwards. Only values set in the profile are expanded, never a command's own `default:`.
- An unknown command or param is a warning.

## State: logs and usage

- `~/.local/state/lazycmd/logs/<job>.log` holds each run's output (as the program wrote it).
- `~/.local/state/lazycmd/trust.json` holds the repos whose commands you allowed, see [above](#allowing-a-repos-commands).
- `~/.local/state/lazycmd/usage.json` holds decayed run counts per repo and command id. It only orders the Global group; delete it to reset. A missing or corrupt file just starts fresh.

## Editor support: JSON Schemas

The files in `schema/` describe every YAML file above, for editor validation and autocomplete. Reference one from a YAML file:

```yaml
# yaml-language-server: $schema=/path/to/lazycmd/schema/lazycmd-commands.schema.json
```

| Schema | For |
|---|---|
| `lazycmd-commands.schema.json` | sidecars and `commands.yaml` |
| `lazycmd-config.schema.json` | `config.yaml` (global and per repo) |
| `lazycmd-profile.schema.json` | `projects/<name>.yaml` |

## Development

```bash
make build      # ./lazycmd
make install    # lazycmd + lc into GOBIN/GOPATH/bin
make test       # go test ./...
```

Design notes and open questions are in [plan.md](plan.md).
