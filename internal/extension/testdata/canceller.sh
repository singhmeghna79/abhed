#!/bin/bash
while IFS= read -r line; do
  case "$line" in
    *'"event":"before_compact"'*) echo '{"cancel":true}' ;;
    *) echo '{}' ;;
  esac
done
