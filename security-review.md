# Security review of lazycmd

Second security review, 2026-11-27. All five findings fixed on 2026-11-27 (after v0.5.1), plus two found in the `lc <script>` review:

- **`lc` secrets:** a `secret` given as `name=value` is refused (it would show in `ps` and shell history); an empty secret keeps an `LC_<NAME>` already in the environment.
- **Sidecar `run:` beside a script:** refused, since the preview and confirm showed the `run:` body while the script ran.

## Medium

### 1. Opening a repo can hang lazycmd (DoS, confirmed)

**Fixed:** `internal/safefile` (non-blocking open, regular files only, 4 MB cap) is used for every repo file read, including `decodeStrict` and `trust.Fingerprint`. Tests with a `/dev/zero` symlink and FIFOs.

**What's wrong:** Files that a repo controls are read with `os.ReadFile` and `io.Copy` without checking that they are regular files or capping their size. A special file in place of a normal one can block the read forever or make it grow without limit. This happens at startup, before the user selects anything, which breaks the README promise that opening a repo is safe. No code is executed.

Two cases were found:
- If `package.json` is a symlink to `/dev/zero`, npm discovery reads forever and memory grows.
- If `.lazycmd/scripts/x` is a FIFO, `trust.Fingerprint` blocks forever.

The same pattern appears in the other repo-controlled files: `pom.xml`, `pyproject.toml`, compose files, `commands.yaml`, sidecars, and `.git/HEAD`.

**Where:**
- `cmd/lazycmd/load.go` (`load()`, runs at startup)
- `internal/trust/trust.go` (`add()` helper, uses `os.Open` and `io.Copy`, called from `Fingerprint`)
- `internal/features/*.go` (npm, maven, compose, python, and other discovery)
- `internal/config/*.go` (`commands.yaml`, sidecars, and other config reads)

**How it was verified:** Confirmed. In a throwaway test, with `package.json` symlinked to `/dev/zero`, `features.Discover` was still running after 3 seconds. The FIFO case was reproduced in the same way for `trust.Fingerprint`.

**Suggested fix:**
- Add one shared reader that calls `Stat()` on the path, accepts only regular files, and reads through `io.LimitReader` with a cap of a few MB.
- Use this reader everywhere repo files are read, including `Fingerprint`. Hash unreadable or non-regular files as `"unreadable"`.
- Add tests using a `/dev/zero` symlink and a FIFO.

### 2. Job logs readable by other local users (confirmed by ls)

**Fixed:** logs folder `0700`, log files `0600`, `usage.json` `0600`; `jobs.PrepareState` at startup sets the state folder and logs folder to `0700` and keeps the newest 200 logs.

**What's wrong:** The state directory and its files are created with permissions that let other local users read them. Once created, the directory stays that way, so whichever code runs first sets the mode. The logs contain the full output of every job, including any tokens that tools print. Logs are never pruned.

On this Mac the logs are protected only because `~/.local/state` is `0700`. On Linux with a `0755` home directory, any local user can read every job's full output.

**Where:**
- `internal/jobs/job.go:102`: creates `stateDir/logs` with `0o755`, and log files with `os.Create` (mode `0644`).
- `internal/usage/log.go`: creates `stateDir` with `0o755`, and `usage.json` with `0o644`.

**How it was verified:** Confirmed by `ls`, which showed the directory as `0755` and the files as `0644`. Whichever of the two packages runs first sets the directory mode to `0755`.

**Suggested fix:**
- Create directories with `MkdirAll` using `0o700`.
- Create log files with `OpenFile` using `0o600`, and `chmod` any existing files.
- Create `usage.json` with `0o600`.
- Optionally keep only the newest N logs.

## Low

### 3. Profile matched by folder name

**Fixed:** a match by folder name alone still works, but shows a warning asking to add `path:`. README explains why.

**What's wrong:** In `LoadProfile`, a profile that has no `path:` matches any folder with the same base name. A malicious clone named like a real project (for example, in `~/Downloads`) gets that profile's included personal scripts and defaults (for example, `env: prod`). Those scripts then run with that folder as the working directory.

**Where:** `internal/config/project.go` (`LoadProfile`). The README is also relevant.

**How it was verified:** Not recorded as reproduced in this review. The finding comes from reading the code.

**Suggested fix:**
- Warn when a profile matches by name only.
- Or require `path:` for matching.
- Update the README to recommend `path:`.

### 4. Trust checked at load, not at run

**Fixed:** repo commands carry `FromRepo`; right before one runs, the fingerprint is checked again, and a changed repo is held back with a message. Files a script sources stay uncovered, documented in the README.

**What's wrong:** A script that was changed after being allowed can run once without a new prompt. This can happen in the 1 to 2 seconds before the reload poll notices the change. Exploiting it needs local write access to the repo.

By design (as with direnv), the fingerprint does not cover files that a script sources or calls. This is not documented yet.

**Where:** `internal/watch` (1 second poll with debounce).

**How it was verified:** Not recorded as reproduced in this review. The finding comes from reading the code.

**Suggested fix:**
- Recompute the fingerprint for the selected repo command right before starting it.
- Document in the README that files a script sources or calls are not covered by the fingerprint.

### 5. CI supply chain

**Fixed:** gitleaks 8.30.1 with its sha256 pinned and checked; actions pinned to commit SHAs (checkout v7.0.1, setup-go v7.1.0, codeql-action v4.39.5), kept current by Dependabot.

**What's wrong:**
- The security workflow downloads the gitleaks tarball with `curl` and does not check a checksum.
- The actions are pinned to version tags, not to commit SHAs.

**Where:**
- `.github/workflows/security.yml`
- Action references: `actions/checkout@v4`, `actions/setup-go@v5`, `github/codeql-action@v3`

**How it was verified:** Not recorded as reproduced in this review. The finding comes from reading the workflow file.

**Suggested fix:**
- Verify the release sha256 against the checksums file published with the gitleaks release.
- Pin each action to a commit SHA. Dependabot keeps the pins up to date.

## Checked and fine

- **Shell quoting** of repo-derived values in all features: npm, maven, compose, python, generated case scripts, and `shellQuote` in `internal/features/npm.go`.
- **Trust prompt and fingerprint coverage** of `commands.yaml`, scripts, and targets.
- **Ask socket:** private `0700` directory, per-job random token, request size limit.
- **Terminal output:** `OutputBuffer` keeps only SGR sequences, and repo text passes through `safetext` before it is drawn.
- **Secrets** are never stored in `last.json` or passed as arguments.
- **Allowlists** for `compose.command`, `python.runner`, and `label`.
- **Regex validation** of maven goals.
- **Package manager** is chosen by lockfile only.
- **Log file names** are sanitised against path traversal.
