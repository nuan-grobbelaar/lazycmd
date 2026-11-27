#!/bin/sh
# Inline demo: this script has no sidecar beside it. Its form comes from
# a profile (examples/projects/inline-demo.yaml, under sidecars:), the way
# you'd give a form to a script in a repo you can't or don't want to
# change. Without that profile, `lc examples/inline/release.sh` just runs
# it, and it complains that no version was given.
set -eu
version="${1:?no version given (copy examples/projects/inline-demo.yaml to get a form)}"
echo "would tag $version and ${LC_PUSH:-false} = push"
