#!/bin/sh
# Inline demo: run with `lc examples/inline/greet.sh`. The sidecar beside
# this script gives it a form and a confirm question; the answers arrive
# as arguments (from the sidecar's args:) and as LC_<NAME> variables.
set -eu

name="${1:?usage: greet.sh <name>}"
echo "Hello, $name! (mood: ${LC_MOOD:-?})"

# lazycmd ask works here too: outside the TUI it asks on this terminal.
if command -v lazycmd >/dev/null 2>&1; then
  answer=$(lazycmd ask "Anything else to say?" --default "nope")
  echo "You said: $answer"
fi

# The exit status is this script's own, since lazycmd exec'd it: try
# `echo $?` afterwards.
exit "${LC_EXIT_CODE:-0}"
