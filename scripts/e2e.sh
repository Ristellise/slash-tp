#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$work/tp"
go build -o "$bin" ./cmd/tp

src="$work/src"
mkdir -p "$src/sub/deep" "$src/empty-dir"
printf '' > "$src/zero.txt"
printf 'hello tp' > "$src/one.bin"
head -c 250000 /dev/urandom > "$src/sub/deep/two.bin"
head -c 9000000 /dev/urandom > "$src/sub/three.bin"
head -c 1000000 /dev/urandom > "$src/ファイル です.txt"
chmod 755 "$src/one.bin"

many="$src/many"
mkdir -p "$many"
for i in $(seq 1 2000); do
  printf 'file %d' "$i" > "$many/f$i.txt"
done
head -c 3000000 /dev/urandom > "$many/bulk.bin"

codes="$work/codes"
"$bin" send --code-file "$codes" "$src" >"$work/sender.log" 2>&1 &
sender=$!

"$bin" receive --code-file "$codes" "$work/dst" >"$work/receiver.log" 2>&1
wait "$sender"

diff -r -x empty-dir "$src" "$work/dst/src"
[ -x "$work/dst/src/one.bin" ]

grep -q 'done:' "$work/sender.log"
grep -q 'done:' "$work/receiver.log"

# second transfer: identical tree is skipped, modified file is updated
printf 'hello tp v2' > "$src/one.bin"
head -c 100000 /dev/urandom > "$src/sub/deep/updated.bin"

codes2="$work/codes2"
"$bin" send --code-file "$codes2" "$src" >"$work/sender2.log" 2>&1 &
sender2=$!

"$bin" receive --code-file "$codes2" "$work/dst" >"$work/receiver2.log" 2>&1
wait "$sender2"

diff -r -x empty-dir "$src" "$work/dst/src"
grep -Eq 'done: [0-9]+ files \([0-9]+ skipped\)' "$work/sender2.log"
grep -q 'skipped' "$work/receiver2.log"

# regression: default relative outdir (".") must accept single-file sends
codes3="$work/codes3"
rel="$work/rel"
mkdir -p "$rel"
(
	cd "$rel"
	"$bin" receive --code-file "$codes3"
) >"$work/rel-r.log" 2>&1 &
relr=$!
"$bin" send --code-file "$codes3" "$src/one.bin" >"$work/rel-s.log" 2>&1
wait "$relr"
[ -f "$rel/one.bin" ]
grep -q 'done:' "$work/rel-r.log"

echo "e2e ok"
