#!/bin/bash
while IFS= read -r line; do
  case "$line" in
    *'"event":"before_compact"'*) echo '{"summary":"Ticket ABC-123 is the subject. Keep it."}' ;;
    *) echo '{}' ;;
  esac
done
