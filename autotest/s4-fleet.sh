#!/bin/bash
# S4 并发建连压测 fleet：串行预建连接 → N worker 同时建连（SSH+VNC 并发计时）→ docker stats 采样 → 串行清理
# 用法: bash s4-fleet.sh <N> <round>     如: bash s4-fleet.sh 5 1
# 产出: /tmp/s4/s4_n<N>_r<round>_* (worker out + setup log + stats csv)；worker 结果 JSON 在 /tmp/perf-*.json
set -u
N=$1
RND=$2
cd "$(dirname "$0")"
OUTDIR=/tmp/s4
mkdir -p "$OUTDIR"
BASE_L=s4_n${N}_r${RND}
export BASE_URL=${BASE_URL:-https://127.0.0.1:5588}

node s4-setup-conns.js create "$BASE_L" "$N" > "$OUTDIR/$BASE_L.setup.log" 2>&1 || { echo "setup failed"; cat "$OUTDIR/$BASE_L.setup.log"; exit 1; }

: > "$OUTDIR/$BASE_L.stats.csv"
(
  while true; do
    docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}}' \
      pyterm_md pyterm_wragent pyterm_wragent2 pyterm_wrgateway 2>/dev/null >> "$OUTDIR/$BASE_L.stats.csv" || break
    echo "---" >> "$OUTDIR/$BASE_L.stats.csv"
    sleep 2
  done
) &
SAMPLER=$!

START_ISO=$(date '+%F %T')
T0=$(date +%s)
PIDS=()
for i in $(seq 1 "$N"); do
  WI=$(printf 'w%02d' "$i")
  L=${BASE_L}_${WI}
  PHASES=connect PERF_PROGRESS="$OUTDIR/$BASE_L.progress.log" \
    node perf-probe.js "$L" > "$OUTDIR/$L.out" 2>&1 &
  PIDS+=($!)
done
FAIL=0
for p in "${PIDS[@]}"; do wait "$p" || FAIL=$((FAIL + 1)); done
T1=$(date +%s)
kill "$SAMPLER" 2>/dev/null
wait "$SAMPLER" 2>/dev/null

node s4-setup-conns.js delete "$BASE_L" "$N" >> "$OUTDIR/$BASE_L.setup.log" 2>&1
echo "fleet n=$N round=$RND start=$START_ISO elapsed=$((T1 - T0))s failed_workers=$FAIL"
