#!/usr/bin/env python3
"""S6 大流量隧道/测速驱动（Agent↔Agent speedtest via md WS）+ pprof 快照

用法: python3 speedtest-run.py --tag A_v102 [--limits 10,50,100,0] [--rounds 3]
产出: /tmp/s6/<tag>/rounds.json + /tmp/s6/<tag>/<container>_{allocs,heap}_r<i>_L<limit>_{pre,post}.prof
环境: S6_BASE(默认 https://127.0.0.1:5588) S6_SRC(local-agent) S6_DST(local-agent-2)
"""
import argparse
import asyncio
import json
import os
import ssl
import subprocess
import time
import urllib.request

import websockets

BASE = os.environ.get("S6_BASE", "https://127.0.0.1:5588")
WSU = BASE.replace("https://", "wss://").replace("http://", "ws://") + "/api/ws/webrtc"
SRC = os.environ.get("S6_SRC", "local-agent")
DST = os.environ.get("S6_DST", "local-agent-2")
CONTAINERS = (("pyterm_wragent", 6061), ("pyterm_wragent2", 6062))


def login():
    ctx = ssl._create_unverified_context()
    req = urllib.request.Request(
        BASE + "/api/auth/login",
        data=json.dumps({"username": "admin", "password": "change_me_pass"}).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    return json.load(urllib.request.urlopen(req, context=ctx))["token"]


def wss_ctx():
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return ctx


def pprof_snapshots(tag, name):
    d = f"/tmp/s6/{tag}"
    os.makedirs(d, exist_ok=True)
    for cname, port in CONTAINERS:
        for prof in ("allocs", "heap"):
            remote = f"/tmp/snap_{prof}.prof"
            local = f"{d}/{cname}_{prof}_{name}.prof"
            try:
                subprocess.run(
                    ["docker", "exec", cname, "wget", "-qO", remote,
                     f"http://127.0.0.1:{port}/debug/pprof/{prof}"],
                    check=True, timeout=30, capture_output=True)
                subprocess.run(["docker", "cp", f"{cname}:{remote}", local],
                               check=True, timeout=30, capture_output=True)
            except Exception as e:
                print(f"[pprof] fail {cname}/{prof}/{name}: {e}", flush=True)


async def one_round(token, limit, timeout=90):
    rec = {"limit": limit, "ok": False}
    try:
        async with websockets.connect(WSU, ssl=wss_ctx(), max_size=2 ** 22,
                                      open_timeout=15) as ws:
            await ws.send(json.dumps({
                "type": "speedtest_start", "source": SRC, "target": DST,
                "mbps_limit": limit, "token": token}))
            t0 = time.time()
            progress = []
            while time.time() - t0 < timeout:
                raw = await asyncio.wait_for(ws.recv(), timeout=timeout)
                try:
                    msg = json.loads(raw)
                except Exception:
                    continue
                t = msg.get("type", "")
                if t == "speedtest_started":
                    rec["room"] = msg.get("room_id")
                    rec["started_at"] = round(time.time() - t0, 3)
                elif t == "speedtest_ping":
                    rec["ping_ms"] = msg.get("ping_ms")
                    rec["jitter_ms"] = msg.get("jitter_ms")
                    rec["loss_pct"] = msg.get("loss_pct")
                elif t == "speedtest_progress":
                    if len(progress) < 500:
                        progress.append([round(time.time() - t0, 2),
                                         msg.get("phase"), msg.get("mbps")])
                elif t == "speedtest_result":
                    rec.update({k: msg.get(k) for k in
                                ("up_mbps", "down_mbps", "up_bytes", "down_bytes", "duration")})
                    rec["elapsed"] = round(time.time() - t0, 3)
                    rec["ok"] = True
                    rec["progress_head"] = progress[:6]
                    rec["progress_tail"] = progress[-6:]
                    return rec
                elif t in ("speedtest_error", "speedtest_cancelled"):
                    rec["error"] = msg.get("detail") or t
                    return rec
                elif t == "speedtest_stop":
                    rec["error"] = "stop"
                    return rec
            rec["error"] = f"timeout after {timeout}s"
            return rec
    except Exception as e:
        rec["error"] = f"{type(e).__name__}: {e}"
        return rec


async def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tag", required=True)
    ap.add_argument("--limits", default="10,50,100,0")
    ap.add_argument("--rounds", type=int, default=3)
    a = ap.parse_args()
    limits = [int(x) for x in a.limits.split(",")]
    tag = a.tag
    d = f"/tmp/s6/{tag}"
    os.makedirs(d, exist_ok=True)
    token = login()
    rounds = []
    for rnd in range(1, a.rounds + 1):
        for lim in limits:
            pre = f"r{rnd}_L{lim}_pre"
            pprof_snapshots(tag, pre)
            rec = await one_round(token, lim)
            pprof_snapshots(tag, f"r{rnd}_L{lim}_post")
            rec["round"] = rnd
            rounds.append(rec)
            print(f"[{tag}] r{rnd} limit={lim} ok={rec.get('ok')} "
                  f"up={rec.get('up_mbps')} down={rec.get('down_mbps')} "
                  f"err={rec.get('error')}", flush=True)
            if not rec.get("ok") and "busy" in str(rec.get("error", "")):
                await asyncio.sleep(6)
            await asyncio.sleep(3)
    with open(f"{d}/rounds.json", "w") as f:
        json.dump(rounds, f, ensure_ascii=False, indent=1)
    ok = sum(1 for r in rounds if r.get("ok"))
    print(f"[{tag}] done {ok}/{len(rounds)} ok", flush=True)


if __name__ == "__main__":
    asyncio.run(main())
