# Examples

Each script shows one or more lazycmd features. They only print, wait or ask questions, so they're safe to run anywhere.

| Script | Shows |
|---|---|
| [`deploy`](scripts/deploy.lazycmd.yaml) | every param type, options with labels (`region`: shows *Frankfurt*, sends `eu-central-1`), a `required` field (`reason`, marked `*` until filled in), `args:`, a `secret` read from the environment, and a **`confirm:`** question after the form |
| [`ask-demo`](scripts/ask-demo.sh) | **`lazycmd ask` / `lazycmd pick`**: a text answer with a default, a pick with labels, a multi pick and a secret, each as a popup mid-run |
| [`pick-file`](scripts/pick-file.lazycmd.yaml) | **`terminal: true`** (marked `⧉`): `enter` hands fzf the real terminal |
| [`panel-size`](scripts/panel-size.sh) | the Output panel's **pseudo-terminal size** and **`LC_PANEL=1`**. Resize the window while it runs |
| [`broken-options`](scripts/broken-options.lazycmd.yaml) | how a failing **`options_cmd`** is reported, and how it clears when `depends_on` changes |
| [`projects/my-app.yaml`](projects/my-app.yaml) | a profile that changes `deploy`'s choices and defaults for one repo |

## Try them in this repo

This repo's `.lazycmd/scripts` is a link to `examples/scripts`, so running `lc` here lists them under **This project**. The first time, lazycmd asks before listing a repo's commands ([trust prompt](../README.md#allowing-a-repos-commands)). Press `y`.

Things to try:

- **`deploy`**: fill in the form and run it. The confirm question shows your answers; `n` goes back to the form, and `enter` does nothing.
- **`ask-demo`**: answer the popups. Run it again, then quickly view another job: its question waits behind a `?` in the Jobs panel until you come back. `esc` on a question cancels, and the script sees exit code 1. Press `t` instead to run it in the real terminal: the same questions are asked there, with fzf for the picks.
- **Run last**: after running `deploy`, press `.` to run it again with the same answers (it still asks the confirm), or select it and press `R`. Because `deploy` has a `secret`, `R` opens the form filled in with your other answers.
- **`pick-file`**: `enter` opens fzf full screen.
- **`panel-size`**: resize the window while it counts.
- **`broken-options`**: pick region `eu` to see the error under the field, then `us` to watch it clear.

## As global scripts

To have them in every repo instead:

```bash
mkdir -p ~/.config/lazycmd/scripts
ln -s "$PWD"/examples/scripts/* ~/.config/lazycmd/scripts/
```

Then copy the profile, set its `path:` to a repo of yours, and open lazycmd there to see `deploy`'s choices change:

```bash
mkdir -p ~/.config/lazycmd/projects && cp examples/projects/my-app.yaml ~/.config/lazycmd/projects/
```
