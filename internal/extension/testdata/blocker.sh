#!/bin/bash
# Blocks any tool call mentioning "secret".
while IFS= read -r line; do
  if echo "$line" | grep -q secret; then
    echo '{"block":true,"reason":"touches a secret"}'
  else
    echo '{}'
  fi
done
