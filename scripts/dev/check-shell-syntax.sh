#!/bin/sh
set -eu

find module scripts tests -type f \( -name '*.sh' -o -path '*/system/bin/*' \) -print | sort | \
while IFS= read -r file; do
  [ -f "$file" ] || continue
  first=$(sed -n '1p' "$file")
  case $first in '#!'*) sh -n "$file" ;; esac
done
