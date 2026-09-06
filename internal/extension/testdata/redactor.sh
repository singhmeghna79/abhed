#!/bin/bash
# Rewrites tool results, and drops the first message from context.
while IFS= read -r line; do
  case "$line" in
    *tool_result*) echo '{"content":"[redacted]"}' ;;
    *\"event\":\"context\"*) echo '{"keep":[1]}' ;;
    *) echo '{}' ;;
  esac
done
