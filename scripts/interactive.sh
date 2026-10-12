#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin=${TP_BIN:-"$PWD/tp"}

head -c 5000000 /dev/urandom > "$work/single"
chmod 640 "$work/single"

( "$bin" send --code-file "$work/codes" "$work/single" > "$work/sender.log" 2>&1 ) &
sender=$!

for _ in $(seq 1 200); do
  [ -f "$work/codes.offer" ] && break
  sleep 0.3
done
offer=$(cat "$work/codes.offer")

( printf '%s\n' "$offer" | timeout 120 "$bin" receive "$work/dst" > "$work/recv.log" 2>&1 ) &
receiver=$!

answer=""
for _ in $(seq 1 200); do
  answer=$(grep '^tp[0-9]#' "$work/recv.log" || true)
  [ -n "$answer" ] && break
  sleep 0.3
done
printf '%s\n' "$answer" > "$work/codes.answer"

wait "$receiver"
wait "$sender"
cmp "$work/single" "$work/dst/single"
[ "$(stat -c %a "$work/dst/single")" = "640" ]
grep -q 'done:' "$work/sender.log"
echo "interactive + single-file ok"
