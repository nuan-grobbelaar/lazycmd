#!/bin/sh
# Inline demo: no sidecar, so `lc examples/inline/plain.sh a "b c"` runs
# this straight away with the arguments as given. No form, no questions.
echo "got $# argument(s):"
for a in "$@"; do
  echo "  [$a]"
done
