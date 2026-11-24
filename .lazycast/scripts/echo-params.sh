#!/bin/sh
set -eu

echo "message:     ${LC_MESSAGE:-<empty>}"
echo "environment: ${LC_ENVIRONMENT:-<empty>}"
echo "features:    ${LC_FEATURES:-<empty>}"
echo "confirm:     ${LC_CONFIRM:-<empty>}"
if [ -n "${LC_API_KEY:-}" ]; then
  echo "api_key:     <set, ${#LC_API_KEY} chars>"
else
  echo "api_key:     <empty>"
fi
echo "notes:"
echo "${LC_NOTES:-<empty>}"
