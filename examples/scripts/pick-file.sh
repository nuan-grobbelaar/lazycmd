#!/bin/sh
# Demo for terminal: true. fzf needs a real terminal, so this command's
# sidecar sets terminal: true and enter hands it the whole screen.
# Without fzf, `lazycmd pick` asks with a numbered list instead.
set -u
cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

if command -v fzf >/dev/null 2>&1; then
  file=$(git ls-files | fzf --prompt "File to preview> " --height 60%) || exit 0
else
  file=$(git ls-files | head -20 | lazycmd pick "File to preview?") || exit 0
fi

echo "== $file =="
head -20 "$file"
printf '\n(press enter to go back to lazycmd) '
read -r _
