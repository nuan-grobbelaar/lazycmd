#!/bin/sh
# Example lazycmd script: a pretend deploy that only prints what it would
# do, so it's safe to run anywhere. It shows both ways params arrive:
#
#   - as arguments, from the sidecar's args: ($1 is the env, then flags)
#   - as LC_<NAME> environment variables, which every param also gets
#
# The token is a secret param, so it can only come from the environment:
# lazycmd refuses to put secrets in args, where `ps` could show them.
set -eu

env="${1:?usage: deploy.sh <env> [--tag=TAG] [--dry-run=true|false]}"
shift

tag="latest"
dry_run=true
for arg in "$@"; do
  case "$arg" in
    --tag=*) tag="${arg#--tag=}" ;;
    --dry-run=*) dry_run="${arg#--dry-run=}" ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

services="${LC_SERVICES:-}"
if [ -z "$services" ]; then
  echo "no services picked, nothing to do"
  exit 0
fi

echo "env:      $env"
echo "region:   ${LC_REGION:-<none>}"
echo "tag:      $tag"
echo "reason:   ${LC_REASON:-}"
echo "dry run:  $dry_run"
if [ -n "${LC_TOKEN:-}" ]; then
  echo "token:    set (${#LC_TOKEN} chars)"
else
  echo "token:    not set"
fi
echo

# A multi param arrives comma-joined: "api,web".
old_ifs=$IFS
IFS=,
for svc in $services; do
  IFS=$old_ifs
  if [ "$dry_run" = true ]; then
    echo "would deploy $svc:$tag to $env"
  else
    echo "deploying $svc:$tag to $env..."
    sleep 1
    echo "  $svc done"
  fi
done
