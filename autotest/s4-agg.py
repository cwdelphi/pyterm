#!/usr/bin/env python3
"""S4 并发建连结果聚合：worker OUT JSON + connection_timeline + docker stats"""
import json, glob, subprocess, re, sys

def pct(a, p):
    if not a:
        return None
    s = sorted(a)
    return s[min(len(s) - 1, -(-int(p * 100) * len(s) // 100) - 1)]

def sql(q):
    o = subprocess.run(
        ["docker", "exec", "pyterm_mariadb", "mariadb", "-uppy", "-pchange_me_pass", "ppy_tools", "-N", "-e", q],
        capture_output=True, text=True)
    return o.stdout.strip()

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
    return v  # MiB / MB

def parse_stats(path):
    cpu, mem = {}, {}
    try:
        lines = open(path).read().splitlines()
    except FileNotFoundError:
        return cpu, mem
    for ln in lines:
        if ln.startswith("---") or not ln.strip():
            continue
        parts = ln.split(",")
        if len(parts) < 3:
            continue
        name, c, m = parts[0], parts[1], parts[2]
        try:
            cpu[name] = max(cpu.get(name, 0), float(c.replace("%", "")))
        except ValueError:
            pass
        v = parse_mem(m)
        if v is not None:
            mem[name] = max(mem.get(name, 0), v)
    return cpu, mem

def workers(N, r):
    out = []
    for f in sorted(glob.glob(f"/tmp/perf-s4_n{N}_r{r}_w*.json")):
        try:
            out.append(json.load(open(f)))
        except Exception as e:
            print(f"  ! bad json {f}: {e}", file=sys.stderr)
    return out

def collect(N, r=None):
    ssh_ms, vnc_ms, sok, vok, sn = [], [], 0, 0, 0
    rounds = (1, 2, 3) if r is None else (r,)
    for rr in rounds:
        for d in workers(N, rr):
            c = d.get("connect") or {}
            s, v = c.get("ssh") or {}, c.get("vnc") or {}
            sn += 1
            if s.get("ok"):
                sok += 1
                ssh_ms.append(s.get("ms", 0))
            if v.get("ok"):
                vok += 1
                vnc_ms.append(v.get("ms", 0))
    return dict(sn=sn, sok=sok, vn=sn, vok=vok, ssh=ssh_ms, vnc=vnc_ms)

print(f"{'N':>3} | {'ssh':>7} {'p50':>7} {'p95':>7} {'max':>7} | {'vnc':>7} {'p50':>7} {'p95':>7} {'max':>7} | {'db':>7} {'ok':>4} {'P2P':>4} {'rly':>4} {'BUG':>4} | {'mdCPU':>6} {'agCPU':>6} {'gwCPU':>6} {'mdMEM':>6} {'agMEM':>6}")
for N in (1, 5, 10, 20):
    a = collect(N)
    db = sql(f"SELECT COUNT(*), COALESCE(SUM(success),0), COALESCE(SUM(webrtc_path='P2P'),0), "
             f"COALESCE(SUM(webrtc_path='relay'),0), COALESCE(SUM(webrtc_path='BUG'),0) "
             f"FROM connection_timeline WHERE conn_name LIKE '%s4_n{N}_r%' AND created_at >= '2026-10-02 12:54:00'")
    cols = (db.split("\t") if db else ["0"] * 5)
    cpu, mem = {}, {}
    for r in (1, 2, 3):
        c, m = parse_stats(f"/tmp/s4/s4_n{N}_r{r}.stats.csv")
        for k, v in c.items():
            cpu[k] = max(cpu.get(k, 0), v)
        for k, v in m.items():
            mem[k] = max(mem.get(k, 0), v)
    print(f"{N:>3} | {str(a['sok'])+'/'+str(a['sn']):>7} {pct(a['ssh'],0.5):>7} {pct(a['ssh'],0.95):>7} {max(a['ssh']) if a['ssh'] else '-':>7} | "
          f"{str(a['vok'])+'/'+str(a['vn']):>7} {pct(a['vnc'],0.5):>7} {pct(a['vnc'],0.95):>7} {max(a['vnc']) if a['vnc'] else '-':>7} | "
          f"{cols[0]:>7} {cols[1]:>4} {cols[2]:>4} {cols[3]:>4} {cols[4]:>4} | "
          f"{cpu.get('pyterm_md',0):>5.1f}% {cpu.get('pyterm_wragent',0):>5.1f}% {cpu.get('pyterm_wrgateway',0):>5.1f}% "
          f"{mem.get('pyterm_md',0):>4.0f}M {mem.get('pyterm_wragent',0):>4.0f}M")
    for r in (1, 2, 3):
        b = collect(N, r)
        print(f"      r{r}: ssh {b['sok']}/{b['sn']} p50={pct(b['ssh'],0.5)} p95={pct(b['ssh'],0.95)} max={max(b['ssh']) if b['ssh'] else '-'} | "
              f"vnc {b['vok']}/{b['vn']} p50={pct(b['vnc'],0.5)} p95={pct(b['vnc'],0.95)} max={max(b['vnc']) if b['vnc'] else '-'}")
print()
print("== agent2/gateway peak ==")
for N in (1, 5, 10, 20):
    cpu, mem = {}, {}
    for r in (1, 2, 3):
        c, m = parse_stats(f"/tmp/s4/s4_n{N}_r{r}.stats.csv")
        for k, v in c.items():
            cpu[k] = max(cpu.get(k, 0), v)
        for k, v in m.items():
            mem[k] = max(mem.get(k, 0), v)
    print(f"N={N}: ag2 {cpu.get('pyterm_wragent2',0):.1f}%/{mem.get('pyterm_wragent2',0):.0f}M  gw {cpu.get('pyterm_wrgateway',0):.1f}%/{mem.get('pyterm_wrgateway',0):.0f}M")
print()
print("== load snapshot ==")
print(open("/tmp/s4/load-snapshot.txt").read())
