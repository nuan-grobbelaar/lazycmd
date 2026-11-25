#!/bin/sh
# Demo: panel jobs run on a pseudo-terminal sized to the Output panel, and
# get LC_PANEL=1. Resize the window while this runs and watch the size change.
echo "LC_PANEL=${LC_PANEL:-<not set: not in the panel>}"
i=0
while [ "$i" -lt "${LC_SECONDS:-20}" ]; do
  i=$((i + 1))
  size=$(stty size 2>/dev/null) || size=""
  if [ -n "$size" ]; then
    echo "${size#* } cols x ${size% *} rows  ($i)"
  else
    echo "no terminal size (not running on a terminal)  ($i)"
  fi
  sleep 1
done
