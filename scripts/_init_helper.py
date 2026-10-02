#!/usr/bin/env python3
import subprocess, json, sys, os, time
from datetime import datetime, timezone

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
SNAPSHOT_FILE = os.path.join(SCRIPT_DIR, "config-snapshot.json")
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
DB_CMD = 'docker exec pyterm_mariadb mariadb -u ppy -pchange_me_pass ppy_tools -N -e'


def db(sql):
    r = subprocess.run(DB_CMD + ' "' + sql + '"', shell=True, capture_output=True, text=True)
    if r.returncode != 0 and r.stderr.strip():
        print(f"  [db-err] ...{r.stderr.strip()[-400:]}", file=sys.stderr)
    return r.stdout.strip()


def load_snap():
    with open(SNAPSHOT_FILE) as f:
        return json.load(f)


def save_snap(s):
    with open(SNAPSHOT_FILE, "w", encoding="utf-8") as f:
        json.dump(s, f, ensure_ascii=False, indent=2)


def check_containers(names=None):
    if names is None:
        names = ["pyterm_mariadb", "pyterm_md", "pyterm_nginx", "pyterm_wragent",
                 "pyterm_wrgateway", "pyterm_coturn", "pyterm_test_ssh"]
    ok = True
    for c in names:
        r = subprocess.run(f'docker inspect -f "{{{{.State.Status}}}}" {c}',
                           shell=True, capture_output=True, text=True)
        st = r.stdout.strip() if r.returncode == 0 else "not_found"
        mark = "ok" if st == "running" else "FAIL"
        print(f"  [{mark}] {c}: {st}")
        if st != "running":
            ok = False
    return ok


def get_agents():
    rows = []
    for line in db("SELECT id, name, token FROM agents WHERE is_active=1;").splitlines():
        p = line.split("\t")
        if len(p) >= 3:
            rows.append({"id": p[0], "name": p[1], "token": p[2]})
    return rows


def get_gateways():
    rows = []
    for line in db("SELECT id, name, url, token FROM gateways WHERE is_active=1;").splitlines():
        p = line.split("\t")
        if len(p) >= 4:
            rows.append({"id": p[0], "name": p[1], "url": p[2], "token": p[3]})
    return rows


def get_ssh():
    d = {}
    for line in db("SELECT id, name FROM ssh_connections;").splitlines():
        p = line.split("\t")
        if len(p) >= 2:
            d[p[0]] = p[1]
    return d


def check_online():
    r = subprocess.run("docker logs pyterm_wragent --tail 20 2>&1",
                       shell=True, capture_output=True, text=True)
    a = any(k in r.stdout for k in ["WebRTC", "DataChannel", "connected", "收到Offer"])
    r2 = subprocess.run("docker logs pyterm_wrgateway --tail 20 2>&1",
                        shell=True, capture_output=True, text=True)
    g = "connected to signaling" in r2.stdout
    return a, g


def verify_tokens():
    ok = True
    adb = db("SELECT token FROM agents WHERE id='local-agent';")
    acf = json.load(open(os.path.join(PROJECT_DIR, "wragent/config.json")))["auth_token"]
    if adb == acf:
        print("  [ok] wragent token match")
    else:
        print("  [FAIL] wragent token mismatch")
        ok = False
    gdb = db("SELECT token FROM gateways WHERE id='local-gateway';")
    gcf = json.load(open(os.path.join(PROJECT_DIR, "wrgateway/config.json")))["gateway_token"]
    if gdb == gcf:
        print("  [ok] wrgateway token match")
    else:
        print("  [FAIL] wrgateway token mismatch")
        ok = False
    return ok


cmd = sys.argv[1] if len(sys.argv) > 1 else "deploy-init"

if cmd == "deploy-init":
    print("=== deploy-init ===")
    print("[1/5] check containers")
    check_containers()
    print("[2/5] export DB state")
    agents = get_agents()
    gws = get_gateways()
    ssh = get_ssh()
    for a in agents:
        print(f"  Agent: {a['id']} = {a['name']}")
    for g in gws:
        print(f"  Gateway: {g['id']} = {g['name']}")
    for k, v in ssh.items():
        print(f"  SSH: {k} = {v}")
    admin = db("SELECT COUNT(*) FROM users WHERE username='admin';")
    print(f"  Admin: {'exists' if admin != '0' else 'missing'}")
    print("[3/5] verify tokens")
    verify_tokens()
    print("[4/5] check online")
    ao, go = check_online()
    print(f"  agent={ao} gateway={go}")
    print("[5/5] save snapshot")
    snap = {
        "created_at": datetime.now(timezone.utc).isoformat(),
        "agents": agents, "gateways": gws, "ssh_connections": ssh,
    }
    save_snap(snap)
    print(f"  saved to {SNAPSHOT_FILE}")
    print("=== DONE ===")

elif cmd == "test-init":
    print("=== test-init ===")
    if not os.path.exists(SNAPSHOT_FILE):
        print(f"  [FAIL] snapshot not found: {SNAPSHOT_FILE}")
        sys.exit(1)
    snap = load_snap()
    print(f"  snapshot from: {snap['created_at']}")
    print("[1/5] ensure admin")
    admin = db("SELECT COUNT(*) FROM users WHERE username='admin';")
    if admin == "0":
        print("  creating admin...")
        subprocess.run(
            """curl -sf -X POST http://127.0.0.1:5588/api/auth/register """
            """-H 'Content-Type: application/json' """
            """-d '{"username":"admin","password":"change_me_pass","email":"admin@example.com"}'""",
            shell=True
        )
        uid = db("SELECT id FROM users WHERE username='admin';")
        if uid:
            db(f"UPDATE users SET role='admin' WHERE id='{uid}';")
            print("  promoted to admin")
    else:
        print("  admin exists")
    print("[2/5] cleanup test SSH connections")
    for prefix in ["c_s_", "d_s_", "d_f_", "a_s_", "a_f_", "test_"]:
        d = db(f"DELETE FROM ssh_connections WHERE name LIKE '{prefix}%'; SELECT ROW_COUNT();")
        if d and d != "0":
            print(f"  deleted {prefix}* x{d}")
    print("[3/5] verify baseline")
    for a in snap.get("agents", []):
        c = db(f"SELECT COUNT(*) FROM agents WHERE id='{a['id']}';")
        print(f"  Agent {a['id']}: {'ok' if c != '0' else 'MISSING'}")
    for g in snap.get("gateways", []):
        c = db(f"SELECT COUNT(*) FROM gateways WHERE id='{g['id']}';")
        print(f"  Gateway {g['id']}: {'ok' if c != '0' else 'MISSING'}")
    for sid, sname in snap.get("ssh_connections", {}).items():
        c = db(f"SELECT COUNT(*) FROM ssh_connections WHERE id='{sid}';")
        print(f"  SSH {sname}: {'ok' if c != '0' else 'MISSING'}")
    print("[4/5] check services")
    check_containers(["pyterm_wragent", "pyterm_wrgateway"])
    print("[5/5] check logs")
    r = subprocess.run("docker logs pyterm_wragent --tail 3 2>&1",
                       shell=True, capture_output=True, text=True)
    for l in r.stdout.strip().split("\n")[-3:]:
        print(f"  agent: {l}")
    r2 = subprocess.run("docker logs pyterm_wrgateway --tail 3 2>&1",
                        shell=True, capture_output=True, text=True)
    for l in r2.stdout.strip().split("\n")[-3:]:
        print(f"  gateway: {l}")
    print("=== DONE ===")

elif cmd == "test-restore":
    print("=== test-restore ===")
    if not os.path.exists(SNAPSHOT_FILE):
        print(f"  [FAIL] snapshot not found: {SNAPSHOT_FILE}")
        sys.exit(1)
    snap = load_snap()
    bl_ssh = snap.get("ssh_connections", {})
    print(f"  baseline: {len(bl_ssh)} SSH connections")
    print("[1/6] remove non-baseline SSH")
    cur = get_ssh()
    deleted = 0
    for cid, cname in cur.items():
        if cid in bl_ssh:
            print(f"  keep: {cname} ({cid})")
        else:
            db(f"DELETE FROM ssh_connections WHERE id='{cid}';")
            print(f"  DELETE: {cname} ({cid})")
            deleted += 1
    print(f"  total deleted: {deleted}")
    print("[2/6] verify agents")
    for a in snap.get("agents", []):
        t = db(f"SELECT token FROM agents WHERE id='{a['id']}';")
        print(f"  Agent {a['id']}: {'token match' if t == a['token'] else 'token MISMATCH'}")
    print("[3/6] verify gateways")
    for g in snap.get("gateways", []):
        t = db(f"SELECT token FROM gateways WHERE id='{g['id']}';")
        print(f"  Gateway {g['id']}: {'token match' if t == g['token'] else 'token MISMATCH'}")
    print("[4/6] verify baseline SSH")
    for sid, sname in bl_ssh.items():
        c = db(f"SELECT COUNT(*) FROM ssh_connections WHERE id='{sid}';")
        print(f"  SSH {sname}: {'ok' if c != '0' else 'MISSING'}")
    print("[5/6] verify config tokens")
    verify_tokens()
    print("[6/6] restart services")
    subprocess.run("docker compose restart wragent wrgateway", shell=True, cwd=PROJECT_DIR)
    time.sleep(10)
    ao, go = check_online()
    print(f"  agent online={ao} gateway online={go}")
    cur2 = get_ssh()
    if set(cur2.keys()) == set(bl_ssh.keys()):
        print("  SSH connections match baseline")
    else:
        print(f"  SSH MISMATCH: current={set(cur2.keys())} baseline={set(bl_ssh.keys())}")
    print("=== DONE ===")

elif cmd == "test-clean-users":
    # T0.7 环境债: 清理 pytest 遗留账号
    # 这些账号由 app/tests 每轮运行创建(带时间戳后缀), 长期堆积会:
    #   1) 污染用户列表/搜索;
    #   2) 被 E2E 误当作真实属主(历史事故: inportb 删掉了 local-agent 导致 agent 永久失联)。
    # admin 与 config-snapshot.json 登记的基线资源一律保留。
    print("=== test-clean-users ===")
    TEST_USER_RE = "^(integ|wrtc|speedtest|sshsftp|auth|normal|iso|test|temp)_"
    _gc = "SET SESSION group_concat_max_len=1000000;"
    ids = db(_gc + f" SELECT GROUP_CONCAT(id) FROM users "
             f"WHERE username REGEXP '{TEST_USER_RE}' OR username IN ('inportb');")
    if not ids:
        print("  no leftover test users")
        print("=== DONE ===")
        sys.exit(0)
    id_list = ",".join(f"'{i}'" for i in ids.split(","))
    names = db(_gc + f" SELECT GROUP_CONCAT(username) FROM users WHERE id IN ({id_list});")
    name_list = ",".join(f"'{n}'" for n in names.split(",")) if names else "''"
    print(f"  leftover users: {len(ids.split(','))}  ({names[:120]}...)")

    snap = load_snap() if os.path.exists(SNAPSHOT_FILE) else {}
    keep_agents = {a["id"] for a in snap.get("agents", [])}
    keep_gws = {g["id"] for g in snap.get("gateways", [])}
    keep_ssh = set(snap.get("ssh_connections", {}).keys())

    def not_in(col, keep):
        if not keep:
            return ""
        kw = ",".join(f"'{k}'" for k in sorted(keep))
        return f" AND {col} NOT IN ({kw})"

    # FK: link_items -> link_groups -> users, 其余直接挂 users, 故先子后父
    steps = [
        ("link_items",   f"DELETE FROM link_items WHERE group_id IN (SELECT id FROM link_groups WHERE user_id IN ({id_list}))"),
        ("link_groups",  f"DELETE FROM link_groups WHERE user_id IN ({id_list})"),
        ("documents",    f"DELETE FROM documents WHERE user_id IN ({id_list})"),
        ("ssh_keys",     f"DELETE FROM ssh_keys WHERE user_id IN ({id_list})"),
        ("links",        f"DELETE FROM links WHERE user_id IN ({id_list})"),
        ("sftp_configs", f"DELETE FROM sftp_configs WHERE user_id IN ({id_list})"),
        ("connection_timeline", f"DELETE FROM connection_timeline WHERE user_id IN ({id_list})"),
        ("agents",       f"DELETE FROM agents WHERE owner_id IN ({id_list}){not_in('id', keep_agents)}"),
        ("gateways",     f"DELETE FROM gateways WHERE owner_id IN ({id_list}){not_in('id', keep_gws)}"),
        ("ssh_connections", f"DELETE FROM ssh_connections WHERE user_id IN ({id_list}){not_in('id', keep_ssh)}"),
        ("users",        f"DELETE FROM users WHERE id IN ({id_list})"),
        ("login_attempts", f"DELETE FROM login_attempts WHERE username IN ({name_list})"),
    ]
    total = 0
    for table, sql in steps:
        r = db(sql + "; SELECT ROW_COUNT();")
        last = (r.splitlines() or [""])[-1].strip()
        n = int(last) if last.lstrip("-").isdigit() else 0
        total += n
        if n:
            print(f"  {table}: -{n}")
    print(f"  total rows deleted: {total}")
    print("  audit_log 保留(历史留痕)")
    print("=== DONE ===")
