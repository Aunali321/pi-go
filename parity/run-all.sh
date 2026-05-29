#!/usr/bin/env bash
# Execution-parity harness. Compares this Go port against the published
# @earendil-works/pi-agent-core by running identical inputs through both and
# diffing the results. Requires node + `npm install` in this directory.
set +e
cd "$(dirname "$0")"
REPO=..
BIN=./bin
mkdir -p "$BIN" "$BIN/out"

if [ ! -d node_modules ]; then echo "installing npm deps..."; npm install >/dev/null 2>&1; fi

echo "building go comparison binaries..."
(cd "$REPO" && go build -o "parity/$BIN/dumppayload" ./cmd/dumppayload &&
  go build -o "parity/$BIN/dumpresp" ./cmd/dumpresp &&
  go build -o "parity/$BIN/dumpcompact" ./cmd/dumpcompact &&
  go build -o "parity/$BIN/harnessrun" ./cmd/harnessrun) || { echo "go build failed"; exit 1; }

PASS=0; FAIL=0
norm() { jq -S "$1" ; }
check() { # name leftfile rightfile
  if diff -q "$2" "$3" >/dev/null; then echo "  OK   $1"; PASS=$((PASS+1)); else echo "  DIFF $1"; diff "$2" "$3"; FAIL=$((FAIL+1)); fi
}

echo "== request payloads (build path) =="
for S in base reasoning longcache images thinking orphan multitoolresult deepseek imageonly toolhistory reasoningoff crossmodel erroredmsg idnormalize; do
  node dump.mjs "$S" 2>/dev/null | norm . > "$BIN/out/js.$S.json"
  "$BIN/dumppayload" "$S" 2>/dev/null | norm . > "$BIN/out/go.$S.json"
  check "$S" "$BIN/out/js.$S.json" "$BIN/out/go.$S.json"
done

echo "== response parsing (stream path) =="
pkill -f mock-resp.mjs 2>/dev/null; sleep 0.3
node mock-resp.mjs >/dev/null 2>&1 & MOCK=$!; sleep 0.7
for F in text toolfrag multitool reasoning_content reasoning_field cachetokens lengthfinish contentfilter reasoning_details responsemodel malformed texttool bothreasoning cachewrite choiceusage functioncall nousage lateid; do
  node dump-resp.mjs "$F" 2>/dev/null | norm 'del(.timestamp)' > "$BIN/out/jr.$F.json"
  "$BIN/dumpresp" "$F" 2>/dev/null | norm 'del(.timestamp)' > "$BIN/out/gr.$F.json"
  check "$F" "$BIN/out/jr.$F.json" "$BIN/out/gr.$F.json"
done
kill $MOCK 2>/dev/null

echo "== compaction prepare (cut-point math) =="
node compact-cmp.mjs 2>/dev/null | norm . > "$BIN/out/jc.json"
"$BIN/dumpcompact" 2>/dev/null | norm . > "$BIN/out/gc.json"
check "prepareCompaction" "$BIN/out/jc.json" "$BIN/out/gc.json"

echo "== full harness loop (real 2-turn run, mock LLM) =="
pkill -f "mock.mjs" 2>/dev/null; sleep 0.3
rm -f requests.jsonl
MOCK_OUT=$(pwd)/requests.jsonl node mock.mjs >/dev/null 2>&1 & MOCK=$!; sleep 0.7
node harness-run.mjs >/dev/null 2>&1
"$BIN/harnessrun" >/dev/null 2>&1
kill $MOCK 2>/dev/null
jq -c 'select(.client=="js").body' requests.jsonl | jq -S . > "$BIN/out/hjs.jsonl"
jq -c 'select(.client=="go").body' requests.jsonl | jq -S . > "$BIN/out/hgo.jsonl"
check "harness-loop-bodies" "$BIN/out/hjs.jsonl" "$BIN/out/hgo.jsonl"

echo ""
echo "=== TOTAL: $PASS pass, $FAIL fail ==="
exit 0
