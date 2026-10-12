#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'kill "$spid" 2>/dev/null || true; rm -rf "$work"' EXIT
head -c 1000 /dev/urandom > "$work/f"
./tp send --code-file "$work/c" "$work/f" > "$work/s.log" 2>&1 &
spid=$!
for _ in $(seq 1 100); do [ -f "$work/c.offer" ] && break; sleep 0.3; done
cat "$work/s.log"
echo "OFFER CODE:"
cat "$work/c.offer"
echo "chars: $(tr -d '\n' < "$work/c.offer" | wc -c)"
