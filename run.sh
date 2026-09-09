#!/bin/bash
# Load .env (line by line to handle spaces safely)
set -a
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    ''|\#*) continue ;;
  esac
  export "$line"
done < .env
set +a

exec ./kasion