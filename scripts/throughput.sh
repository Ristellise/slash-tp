#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$PWD/tp"
size=${1:-300000000}   # bytes total, default ~300 MB
files=${2:-3}

mkdir -p "$work/big"
for i in $(seq 1 "$files"); do
  head -c $((size / files)) /dev/zero | tr '\0' 'x' > "$work/big/f$i.dat"
done

( "$bin" send --code-file "$work/codes" "$work/big" > "$work/sender.log" 2>&1 ) &
sender=$!

for _ in $(seq 1 200); do
  [ -f "$work/codes.offer" ] && break
  sleep 0.3
done
offer=$(cat "$work/codes.offer")
echo "code length: ${#offer} chars"

( printf '%s\n' "$offer" | timeout 300 "$bin" receive "$work/dst" > "$work/recv.log" 2>&1 ) &
receiver=$!

answer=""
for _ in $(seq 1 200); do
  answer=$(grep -m1 '^tp[0-9]#' "$work/recv.log" || true)
  [ -n "$answer" ] && break
  sleep 0.3
done
printf '%s\n' "$answer" > "$work/codes.answer"

wait "$receiver"
wait "$sender"

for i in $(seq 1 "$files"); do
  cmp "$work/big/f$i.dat" "$work/dst/big/f$i.dat"
done
tail -1 "$work/sender.log"
tail -1 "$work/recv.log"
