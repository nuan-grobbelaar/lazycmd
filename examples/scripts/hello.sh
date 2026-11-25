#!/bin/sh
# Demo, project half: a project command with the same name as one of your
# global scripts (examples/global/hello.sh, once linked). lazycmd shows it
# in yellow and, the first time you run it in this repo, asks before
# running it instead of yours.
echo "hello ${LC_NAME:-there}, from the PROJECT's hello: $0"
echo "your global hello did not run"
