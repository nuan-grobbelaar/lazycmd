# lazycmd

[![CI](https://github.com/nuan-grobbelaar/lazycmd/actions/workflows/ci.yml/badge.svg)](https://github.com/nuan-grobbelaar/lazycmd/actions/workflows/ci.yml)
[![Security](https://github.com/nuan-grobbelaar/lazycmd/actions/workflows/security.yml/badge.svg)](https://github.com/nuan-grobbelaar/lazycmd/actions/workflows/security.yml)
[![Release](https://img.shields.io/github/v/tag/nuan-grobbelaar/lazycmd?label=release&sort=semver)](https://github.com/nuan-grobbelaar/lazycmd/tags)
[![Go version](https://img.shields.io/github/go-mod/go-version/nuan-grobbelaar/lazycmd)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Homebrew](https://img.shields.io/badge/homebrew-nuan--grobbelaar%2Ftap-FBB040?logo=homebrew&logoColor=white)](https://github.com/nuan-grobbelaar/homebrew-tap)

A lazygit-style terminal UI for running your scripts and project commands. Pick a command, fill in its parameters in a form, and watch it run as a job. Alias: `lc`.

```bash
brew install nuan-grobbelaar/tap/lazycmd   # from the Homebrew tap
```

Or from source:

```bash
make install     # installs lazycmd and the lc alias to GOBIN (or GOPATH/bin)
cd ~/some/repo && lc
lc scripts/deploy.sh   # or run one script right here: its form, then the script
```

Opening lazycmd in a repo never executes anything from that repo's config. Commands only run after you select them, and a repo's own commands are only listed once you [allow them](#allowing-a-repos-commands). Text from the repo (command names, descriptions, the branch name in `.git/HEAD`, script names in `package.json`) has control characters made visible before it is drawn (ESC shows as `^[`), so a repo can't send escape sequences to your terminal. Files from the repo are read only if they're regular files and at most 4 MB, so a `package.json` that is a link to `/dev/zero`, or a named pipe, can't hang lazycmd.

- [Screen and keys](#screen-and-keys)
- [Where commands come from](#where-commands-come-from)
- [Allowing a repo's commands](#allowing-a-repos-commands)
- [Files and folders](#files-and-folders)
- [Command spec](#command-spec)
- [Params](#params)
- [Passing params to a command](#passing-params-to-a-command)
- [Asking before running: confirm](#asking-before-running-confirm)
- [Asking questions from a script: lazycmd ask / pick](#asking-questions-from-a-script-lazycmd-ask--pick)
- [Plain scripts and sidecars](#plain-scripts-and-sidecars)
- [Running one script inline](#running-one-script-inline)
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
| `t` | Commands | run it with the real terminal, for full-screen programs (vim, less, psql). A command with `terminal: true` (marked `⧉`) does this on `enter` |
| `.` | Commands | run the **last command run in this repo** again, with the same answers (like a shell's `!!`) |
| `R` | Commands | run the **selected** command with the answers you last gave it in this repo |
| `/` | Commands | filter by name (`enter` keeps it, `esc` clears it) |
| `enter` | Jobs | show that job in the Output panel |
| `x` | Jobs | stop the job |
| `r` | Jobs | restart it with the same params |
| `?` before a job | Jobs | it's waiting for an answer (`lazycmd ask` / `pick`, or a prompt): view it to answer |
| `/` | Output | search the output (`enter` keeps the search, `esc` clears it) |
| `n` / `N` | Output | next / previous match of the search |
| `i` | Output | focus mode: keys go straight to the running job; `esc` leaves |
| `↑`/`↓`, `pgup`/`pgdn` | Output | scroll (scrolling up stops following; reaching the bottom resumes) |
| `tab` | anywhere | next panel |
| `←`/`→` | anywhere | previous / next panel |
| `alt+1` / `alt+2` / `alt+3` | anywhere | jump to Commands / Jobs / Output |
| `?` | anywhere | help |
| `q`, `ctrl+c` | anywhere | quit (kills jobs still running) |

**Running something again.** `enter` always opens a command's form with its **defaults**. `.` and `R` use the answers you **actually gave** last time in this repo instead, and skip the form:
- They go through the same checks as `enter`: the [trust prompt](#allowing-a-repos-commands), the shadowing warning, and `terminal: true`. A command that isn't listed (e.g. not allowed yet) can't be re-run.
- **Secrets are never saved.** For a command with a `secret` param, the form opens filled in with your other answers, and you enter the secret.
- If an old answer no longer fits (an option is gone, a param was added, a value fails its `pattern`), the form opens filled in with the old answers and says what changed.
- The key bar shows the last command, e.g. `. deploy (12m ago)`.

**The output panel is not a real terminal.** Panel jobs run on a pseudo-terminal sized to the Output panel, resized along with the window, so tools that format to the width behave. They also get `LC_PANEL=1`, so a script can tell it's in the panel and use a simpler mode. Interactive full-screen programs (fzf, vim, `docker compose exec`) need `t` or `terminal: true`. A program that asks the terminal for its background colour or the cursor position (anything built on lipgloss or termenv, including `lazycmd ask`) is answered by lazycmd, so it doesn't sit out a timeout waiting for a terminal that isn't there.

**Searching the output.** `/` in the Output panel opens a search line at the bottom of the panel; the matches are highlighted as you type, the current one in amber, and the panel title shows where you are (`Output · deploy · 3/17`, or `no matches`).

- It searches what the job printed with the colour codes left out, so a word in colour is found like any other. Highlighting keeps the original colours.
- It ignores case unless your query has a capital letter ("smart case"): `error` finds `Error` and `ERROR`, `Error` finds only `Error`.
- `enter` keeps the search; `n` and `N` step to the next and previous match, wrapping round. `esc` clears it. While you type, `q` and the other keys are text, not commands.
- Searching stops the output following, the same as scrolling up; new output is still searched, and the count grows. Scroll back to the bottom to follow again.
- It works on running and finished jobs, and a search stays set when you switch to another job.

**Config reloads by itself.** lazycmd checks the files it read once a second, and when they change (once the change has settled, so a save or a `git pull` isn't caught halfway) it rebuilds the command list the way startup does:

- **Watched:** the global and the repo's `config.yaml`, `scripts/` and `commands.yaml`, your profiles, the scripts any listed command runs (even from outside the repo), and the files features read: `pom.xml` and the poms below it, `package.json` and its workspaces, lockfiles, compose files, `pyproject.toml`, `vite.config.*`. New poms are found within about ten seconds.
- **Kept:** the selection (by command) and the filter. Running jobs and open forms are left alone; a form that's open keeps the command as it was when it opened.
- **Said once:** the key bar and the Output panel's start text say what changed (`Reloaded: 1 added (deploy), 1 changed (build)`) and list any new warnings. A reload that changes no command, like editing a script's body, says nothing.
- **Trust:** if the repo's own commands changed (a script edited after a `git pull`, say), they're [held back again](#allowing-a-repos-commands) and the yellow "review" row comes back. The prompt only opens on its own at startup, and one you're already reading stays open and updates.

On macOS, option+1/2/3 types `¡ ™ £` unless your terminal treats Option as Meta; both work.

**All of a form's hints are on the line at the bottom of the form** (the keys for the field you're on, `←/→ fields`, `alt+enter run now`, `esc cancel`); on a narrow terminal it takes two lines, with the form's own keys on the second so they're never the part that's cut. The same goes for the `lazycmd ask` popups. The bar at the bottom of the screen is blank while one of them is open.

In a params form: `tab`/`shift+tab` or `←`/`→` move between fields, `space` toggles a yes/no field, `enter` confirms a field and submits on the last one, `esc` cancels.

**Text fields are edited on purpose.** A text field (a string, secret or multi-line text) doesn't take keys when you land on it, so `←`/`→` keep paging through the form even over a field that already has a value. Press `space` to start editing (the space isn't typed); the hint line then reads `esc stop editing`, and `←`/`→` move the cursor. `esc` stops editing (a second `esc` cancels the form), and so does moving to another field with `tab` or `enter`. Typing into a field you aren't editing does nothing but say so, and pasted text starts editing. A value that has to start with a space can't be typed first: type it after another character and move the cursor. Selects, yes/no fields and the `lazycmd ask` popups are unchanged.

**Run now:** `alt+enter` (or `ctrl+j`) runs the command straight away with the answers so far, without stepping through the remaining fields. It is refused, with the reason shown in the form, while a required field is empty or a value doesn't match its `pattern`; the form's hint line shows `alt+enter run now` only when the form is ready to run, and while required fields are empty it says how many. Selects and bools use whatever they currently show (their default or first option).

Terminals send `shift+enter` as a plain `enter`, which a terminal UI cannot tell apart, so it can't be used directly. If you want it anyway, make your terminal send a newline for it: in Ghostty `keybind = shift+enter=text:\n`, in iTerm2 a profile key mapping of Shift+Return to "Send Hex Code" `0x0A`. lazycmd then sees `ctrl+j`, which also runs the form.

When a job stops at a prompt (`[y/N]`, `Password:`, …) a popup appears for the active job and takes your answer (`enter` sends, `esc` dismisses; typing is masked when the program turned echo off). A job that isn't the one shown in the Output panel shows `needs input` in the Jobs panel instead of taking focus; select it there to answer.

Output keeps colours and styles but drops screen-control sequences (clear screen, cursor movement, titles), so programs like `vite dev` can't disturb the UI. Tabs are expanded to 8 columns. Scrollback is capped at 10 000 lines.

## Where commands come from

The command list is grouped, top to bottom:

1. **Ad-hoc**: `run command…`, which asks for any shell command line and runs it as a job in the current folder.
2. **Global**: scripts in `~/.config/lazycmd/scripts/`, most-used-in-this-repo first.
3. **This project**: scripts and commands from the repo's `.lazycmd/`, plus anything a [profile](#project-profiles) includes.
4. **Discovered**: one group per detected [feature](#features) (Maven, NPM, Vite, Docker Compose, Python, git), labelled with its source file.

Rules:
- A project command **shadows** a global command with the same name. Such a command is shown in **yellow**, and the first time you run it in a repo lazycmd stops and shows which of your scripts it hides and what it will run instead (`y` runs it, anything else cancels). This happens before its form opens, so nothing from the project runs until you say yes. After the first run in that repo it isn't asked again. Deleting `usage.json` brings the question back. To try it, see the `hello` demo in [`examples/`](examples/README.md#try-them-in-this-repo).
- Feature commands are namespaced (`maven: test`, `npm: lint`) so they never collide with scripts.
- "Most used here" comes from a usage log keyed by repo and command, with a 14-day half-life, so recent runs weigh more.
- Files that fail to parse are skipped and reported as warnings in the Output panel's start text. They never stop lazycmd from starting.

## Allowing a repo's commands

A repo's `.lazycmd/` decides what its commands are called and what they run, so the first time you open a repo that defines commands, lazycmd asks before listing them. The prompt lists each command with what it runs. `y` allows them; `n`, `esc` or `enter` leaves them hidden for now, and a yellow `N not allowed: review…` row in their place reopens the prompt. Everything else (your global scripts, profile includes, discovered features, ad-hoc) works either way.

The answer is remembered per repo with a fingerprint of `commands.yaml`, every file in `.lazycmd/scripts/`, and every script a command points at (even outside `.lazycmd/`). If any of that changes, for example after a `git pull`, lazycmd asks again and says the commands changed. This is checked again right before a repo command runs, so a script changed a moment ago doesn't run before the reload notices: it's held back and you review it first. Files a script itself reads or sources aren't covered, the same as with direnv. The repo's `config.yaml` is not covered: it only switches features and settings, and runs nothing. It can add [maven goals](#maven-goals-teaching-lazycmd-your-plugins), but only plain goal names (no shell syntax), and a repo's own pom already decides what `mvn` does.

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
  last.json                         # your last answers per command, for . and R
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
| `terminal` | bool | `enter` runs it with the real terminal, like `t`, for interactive programs (fzf, vim, psql). Shown as `⧉` in the list |
| `confirm` | bool or string | ask before running: `true` for "Run <name>?", or the question with `{param}` placeholders. See [confirm](#asking-before-running-confirm) |

In `commands.yaml` an entry must set exactly one of `run` or `script`. In a sidecar the script is the file next to it, so a sidecar beside a script can't set `run:` or `script:` (the preview would show one thing while the script runs). `script:` paths are relative to the file that names them; `~` and absolute paths work too.

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
| `name` | required; the param's name, which becomes `$LC_<NAME>` (see [passing](#passing-params-to-a-command)). Letters, digits and `_` only, not starting with a digit, and unique within the command (ignoring case). Otherwise the command is skipped with a warning |
| `type` | `string` (default), `select`, `multi`, `bool`, `secret` or `text` |
| `default` | initial value. Bools take `true`/`false`. For `multi`, a comma-separated list of options to preselect |
| `pattern` | regexp a `string` value must match |
| `required` | `string` only: the form refuses an empty value. The field's title shows an amber `*` while it's empty, and the bottom bar counts the required fields still empty |
| `options` | static choices for `select` / `multi`. Each is a value, or a `label` / `value` pair |
| `options_cmd` | shell command whose output lines become the choices (`value<TAB>label` for a label) |
| `label` | `basename` or `relative`: derive labels from option values that are paths |
| `depends_on` | names of earlier params; when one changes, `options_cmd` runs again |

| Type | Form field |
|---|---|
| `string` | single-line input (with `pattern` / `required` validation) |
| `select` | pick one |
| `multi` | pick several (values arrive comma-joined) |
| `bool` | yes/no (arrives as `true` or `false`) |
| `secret` | input with masked echo; reaches the command only as `LC_NAME`, never in `args`. Left empty, it keeps an `LC_NAME` already set in lazycmd's environment |
| `text` | multi-line input |

`options_cmd` runs in the repo root with a 5 second timeout, with every param answered so far available as `LC_<NAME>`. Results are cached per command and answers, so a dependent select re-runs only when what it depends on changes. It only runs after you select the command, never on startup. Declare a param after the ones it `depends_on`.

If it fails, the field says why, in red: `⚠ options_cmd failed: exit 1: <first line of stderr>`, or `timed out after 5s`. A command that works but prints nothing shows `no options`. A `select` with nothing to pick can't be submitted. The message clears once a `depends_on` change makes the command work.

### Labels: showing something other than the value

When the values are long (full paths, ids), show a label instead. The **value** is still what the command receives (`LC_<NAME>`, `args:`, the preview) and what every default matches. The label is only what the form shows.

```yaml
params:
  - name: repo
    type: select
    options:
      - label: vandag
        value: /Users/me/repos/vandag
      - label: lazycast
        value: /Users/me/repos/lazycast
      - dev                 # a plain value is its own label
    default: /Users/me/repos/vandag   # defaults use values, not labels
```

- **From `options_cmd`:** print `value<TAB>label`. A line without a tab is just the value.
  ```yaml
  options_cmd: |
    for g in ~/repos/*/.git; do d=${g%/.git}; printf '%s\t%s\n' "$d" "${d##*/}"; done
  ```
- **Derived from paths:** `label: basename` shows the last part of each value (`/r/api/pom.xml` → `pom.xml`), and adds the parent folder when two would look the same (`api/pom.xml`, `web/pom.xml`). `label: relative` shows each value relative to the repo root (`/repo/api/pom.xml` or `./api/pom.xml` → `api/pom.xml`). It works for `options` and `options_cmd` alike, and an option with its own label keeps it.
- Labels have control characters made visible like other text from files. Values are passed on exactly.

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

## Asking before running: confirm

For commands you don't want to run by accident:

```yaml
deploy:
  script: ./scripts/deploy.sh
  confirm: "Deploy {services} to {env}?"   # or confirm: true for "Run deploy?"
```

- The question comes **after** the form and **before** anything runs. `y` runs it. `n` / `esc` go back to the form with your answers kept, or just cancel for a command without params. **`enter` does nothing**, so a stray one can't run a deploy.
- `{name}` is filled in from the answers. A `secret` shows as `••••••` and an empty answer as `(none)`. A placeholder that isn't a param of the command is an error.
- It also asks on `.` / `R` re-runs and on `r` restarts in the Jobs panel.
- A [profile](#project-profiles) can add a confirm to any command in one repo, including feature commands and global scripts: `confirm: { "npm: deploy": true }`. It can change a command's question, but can't remove one.

## Asking questions from a script: lazycmd ask / pick

A script can ask something mid-run. Inside lazycmd it opens a popup; anywhere else it asks on the terminal, so the same script works in both:

```bash
name=$(lazycmd ask "Release name?" --default "$(date +%F)") || exit 1
token=$(lazycmd ask "Token?" --secret)
module=$(find . -name pom.xml | lazycmd pick "Module?")
for t in $(printf 'api\nweb\nworker\n' | lazycmd pick "Targets?" --multi); do echo "$t"; done
```

| | |
|---|---|
| `lazycmd ask PROMPT` | a line of text. `--default VALUE` pre-fills it; `--secret` hides what's typed |
| `lazycmd pick PROMPT` | one of the lines on stdin. A line `VALUE<TAB>LABEL` shows the label and prints the value. `--multi` picks several, printed one per line |
| exit code | `0` with the answer on stdout; `1` if cancelled (like fzf); `2` for a usage error |

- **`pick`'s input:** the options must be piped in; run from a terminal with nothing piped, it exits `2`. Blank lines are skipped and spaces around each value and label are trimmed. No options at all exits `1`, as if cancelled, so `|| exit 1` covers an empty list too. Input over 1 MB is cut off.
- **In the panel:** if the job is the one you're viewing, its question pops up straight away. Otherwise the job gets a `?` in the Jobs panel and waits, never taking focus away from what you're doing. View the job to answer. `esc` cancels (the script sees exit code 1). If the job is stopped while asking, the question goes away.
- **Outside lazycmd**, including commands run with `t` or `terminal: true` and [inline runs](#running-one-script-inline), the question is a small form drawn right in the terminal, styled like lazycmd's own: a text input with the default filled in for `ask` (masked for `--secret`), a select with `/` to filter for `pick`. You can type straight away; `enter` answers, `esc` or Ctrl-C cancels (exit 1). Afterwards the form is replaced by one line with the question and the answer (a secret shows as dots). A `pick` of more than 200 options uses [fzf](https://github.com/junegunn/fzf) instead when it's installed. Where the terminal can't draw a form (`TERM=dumb`), `ask` is a plain prompt and `pick` uses fzf or a numbered list.
- **How it works:** each panel job gets `LC_ASK_SOCK`, a socket in a private folder under the state dir (removed when lazycmd exits), and `LC_ASK_TOKEN`, a random token naming the job. Questions travel over that socket, not through the job's output, so nothing a program prints can open or imitate a popup. lazycmd's guess-the-prompt popup stays for programs that don't use `ask`.

## Plain scripts and sidecars

A script with no sidecar is a command with no params. Add `<script-name>.lazycmd.yaml` beside it to give it a description, a form or arguments. The sidecar is a [command spec](#command-spec) without the `commands:` wrapper:

```yaml
# deploy-staging.lazycmd.yaml
desc: Deploy to staging
params:
  - { name: service, type: select, options_cmd: ls services }
```

A broken sidecar produces a warning and that script is skipped. The `.lazycmd.yaml` suffix keeps sidecars from colliding with other YAML files.

A complete example, with a script, its sidecar and a profile that changes its choices for one repo, is in [`examples/`](examples/).

## Running one script inline

```bash
lc scripts/deploy.sh                    # form, confirm, then the script runs here
lc scripts/deploy.sh env=prod tag=v1.2  # the form starts with these filled in
lc ./build.sh --fast                    # no sidecar: runs at once, args passed through
```

`lc <script>` runs one script in the terminal you're in, without opening the TUI. The argument is a path to an executable file. `ask` and `pick` are taken (see [lazycmd ask / pick](#asking-questions-from-a-script-lazycmd-ask--pick)), so run a script with one of those names as `./ask`.

- **With a sidecar**, the script's form shows below your prompt: the same fields, keys and checks as in the TUI, including `options_cmd`, followed by its `confirm` question if it has one. The sidecar is the `<name>.lazycmd.yaml` beside the script, or an entry in the matching [profile's `sidecars:`](#project-profiles), which wins. The profile's `params:`, `defaults:` and `confirm:` for it apply too. Extra arguments are `name=value` presets for the form; an unknown name, or a value that isn't one of a select's options, is an error. A `secret` can't be given this way (it would show in `ps` and your shell history): set it in the environment, `LC_TOKEN=... lc deploy.sh`, and leave the field empty.
- **Without a sidecar** nothing is asked. The script runs straight away with your arguments as they are.
- Either way, lazycmd then **replaces itself with the script** (`exec`). The output stays in your scrollback, Ctrl-C goes to the script, and the exit status is the script's own. `esc` or Ctrl-C in the form cancels with status 130. After the form, one line such as `deploy  $ scripts/deploy.sh prod` is left above the script's output, so you can see what ran.
- Inside the script, [`lazycmd ask` / `lazycmd pick`](#asking-questions-from-a-script-lazycmd-ask--pick) ask on this terminal, as they do outside lazycmd.
- **Without a terminal** (stdin is a pipe, or in CI) the form is skipped. The defaults and `name=value` arguments are the answers, a select with no default takes its first option, and a required field left empty is an error. A script with `confirm` refuses to run, since nothing can answer the question.
- It runs no job: there's no log file, and it doesn't count towards "run last" or usage.

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
| `disable` | list of features to turn off: `maven`, `npm`, `vite`, `compose`, `python`, `git` |
| `features` | list of features to switch back on after a broader file disabled them |
| `maven`, `npm`, `vite`, `compose`, `python`, `git` | per-feature settings (string values) |

All features are **on by default** and only show when their files are detected, so there is nothing to enable. Order of application: global file, then a matching [profile](#project-profiles), then the repo's own file. Settings override key by key; `disable` / `features` apply in that same order.

```yaml
disable: [git]
maven:
  default_profile: local
```

Settings in use: `maven.default_profile` preselects the profile param on Maven commands, and `maven.goals` [adds goals or changes how they're detected](#maven-goals-teaching-lazycmd-your-plugins). `compose.command` picks the compose CLI (`docker compose`, the default, `docker-compose` or `podman compose`), and `python.runner` how Python scripts and tools run (`uv`, `poetry`, `pdm` or `plain`; by default from the lockfile). `npm.label`, `vite.label`, `compose.label` and `python.label` (`folder`, the default, or `path`) choose how [several projects](#several-projects-in-one-repo) are named in the project pick. The `git` block is accepted but has no settings.

## Features

Features only **read files** (never run `mvn`, `npm`, `git` to find out what exists). Opening a repo executes nothing.

| Feature | Detected by | Commands |
|---|---|---|
| `maven` | `pom.xml` at the root, or in folders below it ([several projects](#maven-in-a-monorepo)) | the lifecycle phases (`validate`, `compile`, `test`, `package`, `verify`, `install`, `deploy`); `spotless:check`, `checkstyle:check`, `spotbugs:check`, `pmd:check` when the root pom declares those plugins; and `spring-boot:run`, `quarkus:dev`, `jetty:run` when the root pom **or any module pom** declares that plugin |
| `npm` | `package.json` at the root, or in folders below it ([several projects](#several-projects-in-one-repo)) | one command per script. The runner comes from the lockfile: `pnpm-lock.yaml` → pnpm, `yarn.lock` → yarn, `bun.lockb` → bun, otherwise npm |
| `vite` | `vite.config.*` at the root, or in folders below it ([several projects](#several-projects-in-one-repo)) | `dev`, `preview`, `build` |
| `compose` | `compose.yaml`, `compose.yml`, `docker-compose.yaml` or `docker-compose.yml`, at the root or in folders below it ([several projects](#several-projects-in-one-repo)) | `up`, `down`, `ps`, `logs`, `build`, `restart`, `pull` and `exec`, with the compose file's services as choices |
| `python` | `pyproject.toml` at the root, or in folders below it ([several projects](#several-projects-in-one-repo)) | one command per script (`[project.scripts]`, `[tool.poetry.scripts]`) and task (`[tool.pdm.scripts]`, `[tool.poe.tasks]`); `pytest` when it's configured or a dependency; `ruff check` and `ruff format` when `[tool.ruff]` exists; `mypy` when `[tool.mypy]` exists |
| `git` | `.git` | `fetch --prune`, `pull`, `push`, `stash`, `stash pop`, `worktree list` |

Params added from your files:
- **Maven** with `<modules>`: a `module` select (runs `-pl`) and an `also_make` checkbox (`-am`, only with a module). With `<profiles>`: a `profile` select (`-P`).
- **Maven in a monorepo:** see [below](#maven-in-a-monorepo): a `target` select picks the project and module.
- **Maven run goals** (`spring-boot:run`, `quarkus:dev`, `jetty:run`): module poms are read too, following `<modules>` down a few levels (only paths inside the repo). In a multi-module project a `module` select is required and lists only the modules that declare the plugin (every module when the root does, since children inherit it). There is no `also_make`, since `-am` would try to start the upstream modules too, so run `maven: install` first if the module depends on others. `spring-boot:run` adds a `spring_profiles` field (`-Dspring-boot.run.profiles=`) and `quarkus:dev` a `quarkus_profile` field (`-Dquarkus.profile=`). These are the framework's profiles, separate from Maven's `-P`. A plugin listed only under `<pluginManagement>` doesn't count. For a plugin that comes from a parent pom or a wrapper, see [maven goals](#maven-goals-teaching-lazycmd-your-plugins).
- **npm** with `workspaces` (array form, or `{packages: [...]}`): every script found in the root or any workspace is listed, and a `workspace` picker chooses where to run it (`.` is the root). It uses the manager's own form: `npm run X --workspace NAME`, `pnpm --filter NAME run X`, `yarn workspace NAME run X`, `bun run --filter NAME X`.

- **Docker Compose:** services (and their `profiles:`) are read from the compose file. `up` has `services` (empty means all), `detach` (default on), `build` and, if any service has profiles, a `profile`. `down` has `volumes`. `logs` has a `service` (`(all services)` by default) and `follow`. `build` has `services` and `no_cache`; `restart` and `pull` have `services`. `exec` has a required `service` and a `command` (default `sh`, run as `sh -c` in the container, so quotes and pipes work); it is interactive, so it takes the real terminal (`⧉`, see [`terminal: true`](#command-spec)). Arguments are passed one by one, so a value can't become shell code. A service or profile name that isn't plain (letters, digits, `. _ -`) is skipped.
- **Python:** the runner comes from the lockfile (`uv.lock` → `uv run`, `poetry.lock` → `poetry run`, `pdm.lock` → `pdm run`), or from `python.runner`; with none, programs run in whatever environment is active (tools as `python -m <tool>`). Tasks defined in `[tool.pdm.scripts]` always run through `pdm run`, since only pdm knows them, and poethepoet tasks through the runner's `poe`. `pytest` has a `path` and a `filter` (`-k`). Names from the file are quoted, and one starting with `-` is skipped.

### Several projects in one repo

`npm`, `vite`, `python` and `compose` (and `maven`, [below](#maven-in-a-monorepo)) also look in folders below the root, up to 4 levels down, for `package.json`, a vite config, `pyproject.toml` or a compose file. Hidden folders, `node_modules`, `target`, `build`, `dist`, `out`, `vendor`, `venv`, `env` and `src` are skipped, and symlinked folders aren't followed. So a monorepo works with or without a project at the root.

- **One project at the root** (the usual case): nothing changes.
- **Several projects, or none at the root:** every command of that feature gets a required **`project`** pick, listing the projects that have that command (`npm: dev` lists only the packages with a `dev` script). The command runs in the project's folder, with that project's own package manager, runner or compose file.
- **Naming:** a project is shown by its folder's name (`(root)` for the root). When two projects share a name, those two are shown by their path from the repo root (`a/app`, `b/app`). With `label: path` in the feature's settings, every project is shown by its path:

  ```yaml
  compose:
    label: path     # or folder (the default)
  ```
- **Params that depend on the project** follow the pick: a compose file's services and profiles, an npm package's workspaces. They're filled in once the project is chosen, so choose the project first.
- **Lockfiles:** an npm package or Python project with no lockfile of its own uses the nearest one above it, up to the repo root, so a pnpm or uv monorepo with one lockfile at the root runs everything the same way.
- **npm workspaces** stay as before: a package that another one lists under `workspaces` isn't a project of its own, it's chosen with the `workspace` pick of the package that lists it.
- **Reload:** new project files are noticed within about ten seconds.

### Maven in a monorepo

lazycmd also looks for `pom.xml` in folders below the root (up to 4 levels down), so a repo of independent Maven projects (`services/billing/pom.xml`, `services/orders/pom.xml`) works with or without a pom at the root. Hidden folders, `node_modules`, `target`, `build`, `dist`, `out`, `vendor`, `venv`, `env` and `src` are skipped, and symlinks aren't followed.

- A pom that another pom lists under `<modules>` belongs to it, and is offered as a module. Every other pom is a **project**.
- **One project at the root** (the usual case): nothing changes. You get the `module` and `also_make` params described above.
- **Several projects, or none at the root:** each command gets a required `target` select instead. It lists every project (shown by its folder, `(root)` for the root) and each project's modules (`services/billing › api`). Running it is `mvn -f <project>/pom.xml [-pl <module>] [-am] [-P…] <goal>`; `also_make` still only applies with a module.
- `spotless:check` and the other plugin goals list only the projects whose pom declares the plugin; `spring-boot:run` and the other [run goals](#features) list the modules that declare it (or all of a project's modules when the project's own pom does), and never use `-am`.
- The `profile` select offers every project's profiles together. A profile a project doesn't have is only a warning from `mvn`.
- A pom that sits below the root but isn't one of its modules (a sample or a tool) counts as a project of its own, which switches the repo to the `target` form. If that's not what you want, list it under `<modules>` or move it below a skipped folder like `src`.

### Maven goals: teaching lazycmd your plugins

A built-in goal such as `spring-boot:run` shows up only when a pom lists its plugin directly under `<build><plugins>`. If yours doesn't (the plugin comes from a parent pom or a profile, or it's a company wrapper with another name), say what to look for under `maven.goals`, in `~/.config/lazycmd/config.yaml`, a [profile](#project-profiles) or the repo's `.lazycmd/config.yaml`:

```yaml
maven:
  goals:
    - goal: spring-boot:run            # built in: this adds a way to detect it
      plugin: acme-boot-maven-plugin   # shows up where this artifactId is declared
    - goal: quarkus:dev                # no plugin: always shown in a repo with a pom.xml
    - goal: clean install              # a goal lazycmd doesn't know about
    - goal: acme:serve
      run: true                        # asks for a module, never -am
      profile_param: acme_env          # a form field, passed as -Dacme.env=<value>
      profile_prop: acme.env
```

| Field | Meaning |
|---|---|
| `goal` | required; what `mvn` runs. Several goals go in one string separated by spaces (`clean install`) |
| `plugin` | the `artifactId` that makes the goal show up. Leave it out to show the goal whenever the repo has a pom.xml |
| `run` | the goal starts an application, so it asks for a module and never uses `-am`. A built-in goal keeps its own setting when this is left out; a new goal is not a run goal |
| `profile_param`, `profile_prop` | together: a form field (named `profile_param`) passed as `-D<profile_prop>=<value>`, like the Spring profiles field on `spring-boot:run` |

- A built-in goal keeps its form fields (`spring-boot:run` still has `spring_profiles`) and gains your plugin as another way to be detected. Entries from the global file, profile and repo file are added together.
- A goal that's already a command (`test`, or one you listed twice) isn't added again.
- A goal made only of letters, digits and `. _ : -` is accepted, since it ends up in the `mvn` command; anything else (`; | $ ( )`, quotes) is skipped with a warning, so a repo's config can't smuggle in a shell command. Fields are limited the same way.
- This only changes what is offered. It needs a `pom.xml` somewhere in the repo (see [monorepos](#maven-in-a-monorepo)), and `mvn` runs whatever the pom says. In a monorepo a goal with no `plugin` is offered for every project.

## Project profiles

`~/.config/lazycmd/projects/<name>.yaml` is a per-repo profile kept outside the repo, so nothing has to be committed to a project. It adds commands, sets form defaults and tunes features.

```yaml
path: ~/repos/vandag            # or paths: [...]
include:
  - ~/shared/db/migrate.lazycmd.yaml    # a sidecar (its script is beside it)
  - ~/shared/tools/commands.yaml        # a commands.yaml-shaped file
  - ~/shared/scripts                    # a folder of scripts
  - ./deploy.sh                         # a script (+ sidecar beside it)
sidecars:                               # forms for scripts that live in the repo
  scripts/deploy.sh:                    # path from the repo root
    desc: Deploy a service
    params:
      - { name: env, type: select, options: [staging, prod] }
    args: ["{env}"]
  bin/release: ./vandag/release.lazycmd.yaml   # or a sidecar file, relative to this profile
defaults:
  deploy-staging: { env: staging }
  "maven: test": { module: api, also_make: true }
  deploy:
    env: staging
    service: "${env}-api"
params:
  echo-params:
    features: { options: [logging, metrics, audit], default: [logging, audit] }
disable: [git]
maven:
  default_profile: local
```

| Key | Meaning |
|---|---|
| `path` / `paths` | the repo(s) this profile is for. `~` is expanded; a relative path is relative to the profile file. Matches the repo itself **or any folder below it** |
| `include` | extra commands for "This project" |
| `sidecars` | a form for a script that lives in the repo, without adding a sidecar to the repo |
| `terminal` | list of commands (name or id, like `defaults`) that run with the real terminal on `enter` in this repo, e.g. a global script's sidecar that doesn't set it |
| `confirm` | map of command (name or id) to `true` or a question: ask before running it in this repo. Adds or changes a confirm, never removes one |
| `defaults` | preselected form values |
| `params` | this repo's options and default for a `select` / `multi` param |
| `features`, `disable`, `maven`, … | exactly as in [config.yaml](#configyaml-features-and-settings) |

**Matching.** With no `path`, the file name (without `.yaml`) must equal the repo folder's name, so `vandag.yaml` matches `~/repos/vandag`. That also matches any other folder of that name, a stranger's clone in `~/Downloads` included, which would get your profile's scripts and defaults; so such a match shows a warning until you add `path:`. If several profiles match, the first by file name wins. The header shows `profile <name>` when one matched.

**include.** Each entry is one of: a script (its `<name>.lazycmd.yaml` beside it is picked up); a `<name>.lazycmd.yaml` sidecar (its script sits beside it, or the sidecar sets `run:` / `script:`); a `commands.yaml`-shaped file (any other `.yaml`/`.yml`); or a folder of scripts. A missing or invalid entry is a warning and the rest still load. The repo's own `.lazycmd/` wins over an included command with the same name, and an included command shadows a global one.

**sidecars.** Gives a script in the repo a form (a description, params, args, `confirm`, `terminal`) while the sidecar stays in your config, for a repo that isn't yours to change or a team that doesn't use lazycmd.
- The key is the script's path from the repo root (`scripts/deploy.sh`). It must stay inside the repo, exist and be executable; otherwise that entry is a warning.
- The value is either the [command spec](#command-spec) written inline, or the path of a `.lazycmd.yaml` file (`~` is expanded, a relative path is relative to the profile). Either way it can't set `run:` or `script:`, since it describes the script it's keyed by. Unknown keys are errors, as in any sidecar.
- The command is named after the script (`deploy`, id `project:deploy`) and listed under "This project". If the repo's `.lazycmd/` already lists the same script, the profile's sidecar replaces that entry.
- It's your file pointing at a script you chose, so it doesn't wait for the repo's commands to be [allowed](#allowing-a-repos-commands). The script itself is still the repo's: after a `git pull` it runs whatever the script now says, as it would from a shell.

**defaults.** Keys are a command's display name (`maven: test`) or its id (`global:deploy-staging`, `maven:test`, `project:deploy`), then param names. This works for any command: global, project, included, or a discovered feature command. The values only preselect; you can still change them in the form.
- Values: strings, numbers, bools, and a list for a `multi` param.
- A `select` default, and each value of a `multi` default, must be one of its static options (otherwise it is ignored with a warning).
- `${param}` refers to another param of the same command and is replaced when the form opens, using that param's default. References chain; a cycle or unknown name is a warning and the text is left as written. They do **not** follow what you type into the other field afterwards. Only values set in the profile are expanded, never a command's own `default:`.
- An unknown command or param is a warning.

**params.** Changes a `select` or `multi` param's choices for this repo, without editing the command's own file. That's handy for a global script whose sidecar can't know each project's options. Keyed like `defaults`.
- `options` replaces the param's options (plain values or `label` / `value` pairs). If the param had an `options_cmd`, that is replaced too, and its `depends_on` no longer applies. A profile can't *add* an `options_cmd`, so profiles never become another place where commands run.
- `default` preselects, and is checked against the (new) options. A list works for `multi`, and `${param}` references work as in `defaults`.
- Setting only `options` drops the command's own default if it isn't among them.
- A `defaults:` entry for the same param wins, and is checked against the new options.
- Options on a param that isn't `select` / `multi`, a default that isn't an option, and an unknown command or param are warnings. Only that part is skipped.

## State: logs and usage

- `~/.local/state/lazycmd/logs/<job>.log` holds each run's output (as the program wrote it). The state folder and its logs are readable only by you (folders `0700`, files `0600`), since output can hold tokens; lazycmd fixes the modes of folders an older version created. The newest 200 logs are kept, older ones are deleted when lazycmd starts.
- `~/.local/state/lazycmd/trust.json` holds the repos whose commands you allowed, see [above](#allowing-a-repos-commands).
- `~/.local/state/lazycmd/last.json` (readable only by you) holds, per repo, the answers you last gave each command and which command ran last, for `.` and `R`. Secret params are never saved. Delete it to forget them.
- `~/.local/state/lazycmd/usage.json` (readable only by you) holds decayed run counts per repo and command id. It only orders the Global group; delete it to reset. A missing or corrupt file just starts fresh.

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

The design notes lazycmd was built from are in [plan.md](plan.md).

## License

[MIT](LICENSE) © Nuan Grobbelaar
