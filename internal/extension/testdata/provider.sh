#!/bin/bash
while IFS= read -r line; do
  case "$line" in
    *'"event":"list_tools"'*)
      echo '{"tools":[{"name":"weather","description":"Look up the weather","schema":{"type":"object","properties":{"city":{"type":"string"}}},"mutates":false}]}' ;;
    *'"event":"invoke_tool"'*)
      echo '{"result":"It is raining."}' ;;
    *) echo '{}' ;;
  esac
done
