#!/bin/sh
# Demo: a script asking questions mid-run with `lazycmd ask` / `lazycmd pick`.
# In the lazycmd panel each question is a popup. Run it in a plain terminal
# (or with t in lazycmd) and the same questions are asked on the terminal,
# with fzf for the picks if it's installed.
set -u

echo "Starting a pretend release..."
sleep 1

name=$(lazycmd ask "Release name?" --default "release-$(date +%Y%m%d)") || { echo "cancelled at the name (exit $?)"; exit 1; }
echo "name:     $name"

# value<TAB>label: the popup shows the label, the script gets the value.
env=$(printf 'dev\tDevelopment\nstaging\tStaging\nprod\tProduction (careful)\n' | lazycmd pick "Which environment?") || { echo "cancelled at the environment"; exit 1; }
echo "env:      $env"

echo "services:"
printf 'api\nweb\nworker\nscheduler\n' | lazycmd pick "Which services?" --multi | while read -r svc; do
  echo "  - $svc"
done

token=$(lazycmd ask "Token? (anything; it's never shown)" --secret) || token=""
echo "token:    ${#token} characters"

echo
echo "Done. Nothing was released."
