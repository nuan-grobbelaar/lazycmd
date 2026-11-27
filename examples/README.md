# Examples

Each script shows one or more lazycmd features. They only print, wait or ask questions, so they're safe to run anywhere.

| Script | Shows |
|---|---|
| [`deploy`](scripts/deploy.lazycmd.yaml) | every param type, options with labels (`region`: shows *Frankfurt*, sends `eu-central-1`), a `required` field (`reason`, marked `*` until filled in), `args:`, a `secret` read from the environment, and a **`confirm:`** question after the form |
| [`ask-demo`](scripts/ask-demo.sh) | **`lazycmd ask` / `lazycmd pick`**: a text answer with a default, a pick with labels, a multi pick and a secret, each as a popup mid-run |
| [`pick-file`](scripts/pick-file.lazycmd.yaml) | **`terminal: true`** (marked `⧉`): `enter` hands fzf the real terminal |
| [`panel-size`](scripts/panel-size.sh) | the Output panel's **pseudo-terminal size** and **`LC_PANEL=1`**. Resize the window while it runs |
| [`broken-options`](scripts/broken-options.lazycmd.yaml) | how a failing **`options_cmd`** is reported, and how it clears when `depends_on` changes |
| [`hello`](scripts/hello.sh) + [`global/hello.sh`](global/hello.sh) | a **project command shadowing a global script** of the same name: shown in yellow, and asked about before its first run in a repo. Needs the global half linked (below) |
| [`features/compose-demo`](features/compose-demo/compose.yaml) | the **`compose`** feature: `cd` into it and run `lc` to see `up`, `down`, `logs`, `exec` and the rest with its services (and the `jobs` profile) as choices |
| [`features/maven-monorepo`](features/maven-monorepo/services) | the **`maven`** feature with no root pom: a `target` pick over `billing` (modules `api` and `web`) and `orders`; `spring-boot:run` offers only `billing › api`, `spotless:check` only `orders` |
| [`features/monorepo-demo`](features/monorepo-demo) | **several projects in one repo**: `web` (npm + vite), `api` (python + compose) and `worker` (compose). `npm`, `vite`, `python` and `compose` commands each ask which project, and a compose project's services are listed for the one picked |
| [`features/python-demo`](features/python-demo/pyproject.toml) | the **`python`** feature: a command for each script and task, and `pytest`, `ruff` and `mypy` since they're configured |
| [`inline/`](inline) | **`lc <script>`**: run one script in your terminal without the TUI. `greet` has a sidecar (form, confirm, `lazycmd ask` mid-run, exit status), `plain` has none (runs at once), and `release` gets its form from a profile's **`sidecars:`** ([`projects/inline-demo.yaml`](projects/inline-demo.yaml)) |
| [`projects/my-app.yaml`](projects/my-app.yaml) | a profile that changes `deploy`'s choices and defaults for one repo, and teaches the maven feature a wrapper plugin with **`maven.goals`** (so `spring-boot:run` shows up in a repo whose pom lists `acme-boot-maven-plugin`) |

## Try them in this repo

This repo's `.lazycmd/scripts` is a link to `examples/scripts`, so running `lc` here lists them under **This project**. The first time, lazycmd asks before listing a repo's commands ([trust prompt](../README.md#allowing-a-repos-commands)). Press `y`.

Things to try:

- **`deploy`**: fill in the form and run it. The confirm question shows your answers; `n` goes back to the form, and `enter` does nothing.
- **`ask-demo`**: answer the popups. Run it again, then quickly view another job: its question waits behind a `?` in the Jobs panel until you come back. `esc` on a question cancels, and the script sees exit code 1. Press `t` instead to run it in the real terminal: the same questions are asked there, with fzf for the picks.
- **Run last**: after running `deploy`, press `.` to run it again with the same answers (it still asks the confirm), or select it and press `R`. Because `deploy` has a `secret`, `R` opens the form filled in with your other answers.
- **`pick-file`**: `enter` opens fzf full screen.
- **`panel-size`**: resize the window while it counts.
- **`broken-options`**: pick region `eu` to see the error under the field, then `us` to watch it clear.
- **`hello`** (shadowing): first link the global half, so you have a global script called `hello`:

  ```bash
  mkdir -p ~/.config/lazycmd/scripts && ln -s "$PWD"/examples/global/hello.sh ~/.config/lazycmd/scripts/
  ```

  Open `lc` here: `hello` under **This project** is yellow, and your global `hello` isn't listed. Press `enter`. Before the form opens, lazycmd says which of your scripts it hides and what it runs instead. `y` runs it and opens the form; anything else cancels, and nothing from the project has run. After that first run it isn't asked again in this repo. To see the question again, delete `~/.local/state/lazycmd/usage.json` (which also clears the usage counts). In any other repo, `hello` is your global one. Remove the link to finish: `rm ~/.config/lazycmd/scripts/hello.sh`.

**Inline** (`lc <script>`, no TUI):

```bash
lc examples/inline/greet.sh            # form, confirm, a question mid-run, then: echo $?
lc examples/inline/greet.sh name=Ada   # the form starts with name filled in
lc examples/inline/plain.sh a "b c"    # no sidecar: runs at once
lc examples/inline/greet.sh name=Ada </dev/null   # no terminal: refuses, since greet has a confirm
```

For `release.sh`, copy the profile and set its `path:` to this repo, then run `lc examples/inline/release.sh`: the form comes from the profile's `sidecars:`, though nothing sits beside the script.

```bash
mkdir -p ~/.config/lazycmd/projects && cp examples/projects/inline-demo.yaml ~/.config/lazycmd/projects/
```

**Features:** discovered commands come from the files in the folder you run `lc` in, so for `compose-demo` and `python-demo`:

```bash
cd examples/features/compose-demo && lc    # look under "Discovered — Docker Compose"
```

Nothing starts until you pick a command (the compose ones need Docker).

## As global scripts

To have them in every repo instead (but not in this repo: here each project script would shadow its own global link, and all of them turn yellow):

```bash
mkdir -p ~/.config/lazycmd/scripts
ln -s "$PWD"/examples/scripts/* ~/.config/lazycmd/scripts/
```

Then copy the profile, set its `path:` to a repo of yours, and open lazycmd there to see `deploy`'s choices change:

```bash
mkdir -p ~/.config/lazycmd/projects && cp examples/projects/my-app.yaml ~/.config/lazycmd/projects/
```
