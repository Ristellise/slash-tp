#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$work/tp"
go build -o "$bin" .

src="$work/src"
mkdir -p "$src/sub/deep" "$src/empty-dir"
printf '' > "$src/zero.txt"
printf 'hello tp' > "$src/one.bin"
head -c 250000 /dev/urandom > "$src/sub/deep/two.bin"
head -c 9000000 /dev/urandom > "$src/sub/three.bin"
head -c 1000000 /dev/urandom > "$src/ファイル です.txt"
chmod 755 "$src/one.bin"

codes="$work/codes"
"$bin" send --code-file "$codes" "$src" >"$work/sender.log" 2>&1 &
sender=$!

"$bin" receive --code-file "$codes" "$work/dst" >"$work/receiver.log" 2>&1
wait "$sender"

diff -r "$src" "$work/dst/src"
[ -x "$work/dst/src/one.bin" ]

grep -q 'done:' "$work/sender.log"
grep -q 'done:' "$work/receiver.log"
echo "e2e ok"
