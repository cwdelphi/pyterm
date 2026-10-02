#!/bin/bash
# S6/S8 编排：docker stats 采样 → speedtest-run.py → gctrace 提取
# 用法: bash s6-run.sh <tag> [--limits 10,50,100,0] [--rounds 3]
set -u
TAG=$1
shift
cd "$(dirname "$0")"
mkdir -p "/tmp/s6/$TAG"
START_ISO=$(date -Iseconds)
: > "/tmp/s6/$TAG.stats.csv"
(
  while true; do
    docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}}' \
      pyterm_md pyterm_wragent pyterm_wragent2 pyterm_wrgateway 2>/dev/null >> "/tmp/s6/$TAG.stats.csv" || break
    echo "---" >> "/tmp/s6/$TAG.stats.csv"
    sleep 2
  done
) &
SAMPLER=$!
python3 speedtest-run.py --tag "$TAG" "$@"
RC=$?
kill "$SAMPLER" 2>/dev/null
wait "$SAMPLER" 2>/dev/null
for c in pyterm_wragent pyterm_wragent2; do
  docker logs --since "$START_ISO" "$c" 2>&1 | grep -E "^gc [0-9]+ @" > "/tmp/s6/$TAG.$c.gc" || true
done
echo "s6 done tag=$TAG rc=$RC gc_lines=$(wc -l < /tmp/s6/$TAG.pyterm_wragent.gc 2>/dev/null || echo 0)"
