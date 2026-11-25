# Examples

| File | What it shows |
|---|---|
| [`scripts/deploy.sh`](scripts/deploy.sh) | A plain shell script that reads `$1`, flags and `LC_` env vars. It only prints, so it's safe to run. |
| [`scripts/deploy.lazycmd.yaml`](scripts/deploy.lazycmd.yaml) | Its sidecar: `select`, `multi`, `bool`, `secret` and `string` params, options with labels (`region`), plus `args:` |
| [`projects/my-app.yaml`](projects/my-app.yaml) | A profile that changes the script's choices and defaults for one repo |

To try it as a global script (it shows up in every repo):

```bash
mkdir -p ~/.config/lazycmd/scripts
ln -s "$PWD/examples/scripts/deploy.sh" "$PWD/examples/scripts/deploy.lazycmd.yaml" ~/.config/lazycmd/scripts/
```

Then copy the profile, set its `path:` to a repo of yours, and open lazycmd there to see `services` and `env` change:

```bash
mkdir -p ~/.config/lazycmd/projects && cp examples/projects/my-app.yaml ~/.config/lazycmd/projects/
```
