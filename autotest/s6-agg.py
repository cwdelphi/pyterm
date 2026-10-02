#!/usr/bin/env python3
"""S6/S8 聚合：pprof alloc 差值 + gctrace GC 统计 + docker stats 峰值 + 吞吐汇总
产出 /tmp/s6/agg.txt
"""
import json
import os
import re
import subprocess

GO = "/usr/local/go/bin/go"
TAGS = ["A0_v102", "B0_v101", "C1_nomemlimit", "C2_gogc10", "A_v102", "B_v101"]
MATCHED = ["A0_v102", "B0_v101", "C1_nomemlimit", "C2_gogc10"]  # 全为纯 limit=0 窗口, GC 可公平对比
CONTAINERS = ("pyterm_wragent", "pyterm_wragent2")
LINES = []


def out(*a):
    s = " ".join(str(x) for x in a)
    LINES.append(s)
    print(s)


def pprof_total(pre, post, idx):
    if not (os.path.exists(pre) and os.path.exists(post)):
        return None
    r = subprocess.run(
        [GO, "tool", "pprof", f"-sample_index={idx}", "-top", "-nodecount=1", "-base", pre, post],
        capture_output=True, text=True)
    m = re.search(r"of ([\d.,]+)\s*([KMG]?)(B?) total", r.stdout)
    if not m:
        return None
    v = float(m.group(1).replace(",", ""))
    u, b = m.group(2), m.group(3)
    k = {"": 0, "K": 1, "M": 2, "G": 3}[u]
    base = 1024 if b else 1000
    return v * (base ** k)


def gc_stats(tag, cname):
    p = f"/tmp/s6/{tag}.{cname}.gc"
    if not os.path.exists(p):
        return {}
    pauses, ts = [], []
    for ln in open(p).read().splitlines():
        m = re.search(r"gc (\d+) @([\d.]+)s [^:]*: ([\d.]+)\+([\d.]+)\+([\d.]+) ms clock", ln)
        if m:
            ts.append(float(m.group(2)))
            pauses.append(float(m.group(3)) + float(m.group(4)) + float(m.group(5)))
    if not ts:
        return {}
    win = max(ts[-1] - ts[0], 0.001)
    sp = sorted(pauses)

    def pc(p):
        return sp[min(len(sp) - 1, max(0, int(p * len(sp))))]

    return {"n": len(ts), "win": round(win, 1), "per_s": round(len(ts) / win, 2),
            "avg": round(sum(pauses) / len(pauses), 3), "p99": round(pc(0.99), 3),
            "max": round(max(pauses), 3), "tot": round(sum(pauses), 1)}


def parse_mem(m):
    mm = re.match(r"([\d.]+)\s*(MiB|GiB|kB|MB|B)", m.strip())
    if not mm:
        return None
    v, u = float(mm.group(1)), mm.group(2)
    if u == "GiB":
        return v * 1024
    if u == "kB":
        return v * 1024 / 1048576
    if u == "B":
        return v / 1048576
    return v


def stats_peak(tag):
    cpu, mem = {}, {}
    p = f"/tmp/s6/{tag}.stats.csv"
    if not os.path.exists(p):
        return cpu, mem
    for ln in open(p).read().splitlines():
        if ln.startswith("---") or not ln.strip():
            continue
        parts = ln.split(",")
        if len(parts) < 3:
            continue
        try:
            cpu[parts[0]] = max(cpu.get(parts[0], 0), float(parts[1].replace("%", "")))
        except ValueError:
            pass
        v = parse_mem(parts[2])
        if v is not None:
            mem[parts[0]] = max(mem.get(parts[0], 0), v)
    return cpu, mem


# ── 1. 吞吐 ──
out("== 1. 吞吐 (Mbps, 3轮均值) ==")
all_rounds = {}
for tag in TAGS:
    rs = json.load(open(f"/tmp/s6/{tag}/rounds.json"))
    all_rounds[tag] = rs
    bylim = {}
    for r in rs:
        if r.get("ok"):
            bylim.setdefault(r["limit"], []).append((r["up_mbps"], r["down_mbps"]))
    parts = []
    for lim in sorted(bylim):
        ups = [x[0] for x in bylim[lim]]
        dns = [x[1] for x in bylim[lim]]
        parts.append(f"L{lim}: up {sum(ups)/len(ups):.1f} down {sum(dns)/len(dns):.1f} (n={len(ups)})")
    out(f"  {tag}: " + " | ".join(parts))

# ── 2. GC (gctrace, 仅 wragent 主压测进程; limit=0 轮占比高) ──
out("== 2. gctrace GC 统计 (纯 L0 窗口, wragent; A/B 混档轮已排除) ==")
out(f"  {'tag':<16} {'gc数':>7} {'窗口s':>7} {'gc/s':>7} {'pause均ms':>9} {'p99ms':>8} {'maxms':>8} {'总pause_ms':>10}")
for tag in MATCHED:
    g = gc_stats(tag, "pyterm_wragent")
    g2 = gc_stats(tag, "pyterm_wragent2")
    if g:
        out(f"  {tag:<16} {g['n']:>7} {g['win']:>7} {g['per_s']:>7} {g['avg']:>9} {g['p99']:>8} {g['max']:>8} {g['tot']:>10}")
        if g2:
            out(f"  {'  └ agent2':<16} {g2['n']:>7} {g2['win']:>7} {g2['per_s']:>7} {g2['avg']:>9} {g2['p99']:>8} {g2['max']:>8} {g2['tot']:>10}")

# ── 3. alloc 差值（pprof -base, 两容器合计; 按 limit 聚合3轮） ──
out("== 3. alloc 差值 (pprof -base, wragent+wragent2 合计, 3轮合计) ==")
out(f"  {'tag':<16} {'L':>3} {'alloc_space MB':>15} {'alloc_objects M':>16} {'流量 MB':>9} {'MB分配/MB流量':>14} {'allocs/Kalloc-MB':>17}")
alloc_table = {}
for tag in TAGS:
    rs = all_rounds[tag]
    bylim = {}
    for r in rs:
        if not r.get("ok"):
            continue
        d = f"/tmp/s6/{tag}"
        space = objs = 0.0
        miss = False
        for cname in CONTAINERS:
            s = pprof_total(f"{d}/{cname}_allocs_r{r['round']}_L{r['limit']}_pre.prof",
                            f"{d}/{cname}_allocs_r{r['round']}_L{r['limit']}_post.prof",
                            "alloc_space")
            o = pprof_total(f"{d}/{cname}_allocs_r{r['round']}_L{r['limit']}_pre.prof",
                            f"{d}/{cname}_allocs_r{r['round']}_L{r['limit']}_post.prof",
                            "alloc_objects")
            if s is None or o is None:
                miss = True
            space += s or 0
            objs += o or 0
        e = bylim.setdefault(r["limit"], {"space": 0.0, "objs": 0.0, "bytes": 0.0, "n": 0, "miss": False})
        e["space"] += space
        e["objs"] += objs
        e["bytes"] += (r.get("up_bytes") or 0) + (r.get("down_bytes") or 0)
        e["n"] += 1
        e["miss"] = e["miss"] or miss
    alloc_table[tag] = bylim
    for lim in sorted(bylim):
        e = bylim[lim]
        mb = e["bytes"] / 1048576
        ratio = e["space"] / e["bytes"] * 1048576 if e["bytes"] else 0  # MB alloc per MB moved → 无量纲 (B/B)
        # 表述: 分配字节 / 传输字节 = 每传1MB分配多少MB
        alloc_per_mb = (e["space"] / 1048576) / mb if mb else 0
        kalloc_per_mb = e["objs"] / 1e3 / mb if mb else 0
        flag = " (缺)" if e["miss"] else ""
        out(f"  {tag:<16} {lim:>3} {e['space']/1048576:>15.1f} {e['objs']/1e6:>16.2f} {mb:>9.1f} {alloc_per_mb:>14.2f} {kalloc_per_mb:>17.1f}{flag}")

# ── 4. 容器 CPU/RSS 峰值 ──
out("== 4. docker stats 峰值 ==")
out(f"  {'tag':<16} {'md CPU%':>8} {'agent CPU%':>10} {'agent2 CPU%':>11} {'md MEM':>7} {'agent MEM':>10} {'agent2 MEM':>11}")
for tag in TAGS:
    cpu, mem = stats_peak(tag)
    out(f"  {tag:<16} {cpu.get('pyterm_md',0):>8.1f} {cpu.get('pyterm_wragent',0):>10.1f} {cpu.get('pyterm_wragent2',0):>11.1f} "
        f"{mem.get('pyterm_md',0):>5.0f}M {mem.get('pyterm_wragent',0):>7.0f}M {mem.get('pyterm_wragent2',0):>8.0f}M")

# ── 5. 关键 A/B 对照 ──
out("== 5. 关键对照 (limit=0, 3轮) ==")
for pair, (t1, t2) in {
    "alloc+GC A0(1.0.2池化) vs B0(1.0.1):": ("A0_v102", "B0_v101"),
    "GC A0(GOMEMLIMIT=256MiB) vs C1(无):": ("A0_v102", "C1_nomemlimit"),
    "GC A0(GOMEMLIMIT=256MiB) vs C2(GOGC=10):": ("A0_v102", "C2_gogc10"),
}.items():
    e1, e2 = alloc_table.get(t1, {}).get(0), alloc_table.get(t2, {}).get(0)
    g1, g2 = gc_stats(t1, "pyterm_wragent"), gc_stats(t2, "pyterm_wragent")
    row = []
    if e1 and e2 and e1["space"] and e1["bytes"] and e2["bytes"]:
        r1 = (e1["space"] / 1048576) / (e1["bytes"] / 1048576)
        r2 = (e2["space"] / 1048576) / (e2["bytes"] / 1048576)
        row.append(f"alloc_space {e1['space']/1048576:.0f} vs {e2['space']/1048576:.0f}MB ({(e1['space']/e2['space']-1)*100:+.0f}%)")
        row.append(f"分配MB/传输MB {r1:.2f} vs {r2:.2f} ({(r1/r2-1)*100:+.0f}%)")
    if g1 and g2:
        row.append(f"gc数/3轮 {g1['n']} vs {g2['n']} ({(g1['n']/g2['n']-1)*100:+.0f}%) | gc/s {g1['per_s']} vs {g2['per_s']} | pause_p99 {g1['p99']} vs {g2['p99']}ms")
    out(f"  {pair} " + " | ".join(row))

txt = "\n".join(LINES)
open("/tmp/s6/agg.txt", "w").write(txt)
