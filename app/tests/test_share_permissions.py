"""分享权限 / 菜单收敛 / 机密遮蔽 测试 — S1~S4

覆盖:
- S1 普通角色: /users /roles /audit 仍 403；/agent-id/next 放开 200；/shareable-users 仅 id+username 且排除自己
- S2 非属主对共享 Agent: 配置/启用禁用/删除/分享/token/升级 → 403；查看配置 → 200；查看 shares → 403
- S3 列表对共享来的 Agent 不返回 token（遮蔽为 ""），自有 Agent token 原样；owner_name/is_owner 正确
- S4 /me/permissions 返回的角色权限集合与 ROLE_PERMISSIONS 一致
- S5 共享 Gateway/Coturn 与 Agent 同规则：变更 403、shares/test-credentials 403、token/secret 遮蔽
"""
from fastapi.testclient import TestClient
from app.main import app
from app.auth import ROLE_PERMISSIONS
from app.tests.sync_db import _read_users_file, _write_users_file, register_user, delete_user_by_username

client = TestClient(app)

_TS = int(__import__("time").time())
_OWNER = f"test_shown_{_TS}"
_SHARER = f"test_shshar_{_TS}"
_PASS = "Normal123!"
_AGENT = f"test-share-agent-{_TS}"


def _cleanup_user(username: str):
    delete_user_by_username(username)
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_OWNER)
    _cleanup_user(_SHARER)
    register_user(_OWNER, _PASS, "shown@test.com")
    register_user(_SHARER, _PASS, "shshar@test.com")
    # 属主创建 Agent 并共享给 sharer
    h = _login(_OWNER)
    client.post("/api/admin/agents", json={"id": _AGENT, "name": "share-test", "remark": ""}, headers=h)
    sharer_id = _user_id(_SHARER)
    client.post(f"/api/admin/agents/{_AGENT}/share", json={"shared_with": [sharer_id]}, headers=h)


def teardown_module():
    _gh = _login(_OWNER)
    client.delete(f"/api/admin/agents/{_AGENT}", headers=_gh)
    client.delete(f"/api/admin/gateways/{_GW}", headers=_gh)
    client.delete(f"/api/admin/coturn/{_COTURN}", headers=_gh)
    _cleanup_user(_OWNER)
    _cleanup_user(_SHARER)


def _user_id(username: str) -> str:
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == username:
            return u["id"]
    raise AssertionError(f"user not found: {username}")


def _login(username: str) -> dict:
    resp = client.post("/api/auth/login", json={"username": username, "password": _PASS})
    assert resp.status_code == 200, resp.text
    return {"Authorization": f"Bearer {resp.json()['token']}"}


def _owner_h():
    return _login(_OWNER)


def _sharer_h():
    return _login(_SHARER)


# ════════════════════════════════════════════
#  S1 权限矩阵
# ════════════════════════════════════════════

def test_s1_admin_only_endpoints_403_for_user():
    h = _sharer_h()
    assert client.get("/api/admin/users", headers=h).status_code == 403
    assert client.get("/api/admin/roles", headers=h).status_code == 403
    assert client.get("/api/admin/audit", headers=h).status_code == 403


def test_s1_next_agent_id_opened_to_agent_manage():
    resp = client.get("/api/admin/agent-id/next", headers=_sharer_h())
    assert resp.status_code == 200, resp.text
    assert resp.json()["id"].startswith("wragent-")


def test_s1_shareable_users_only_id_username_exclude_self():
    resp = client.get("/api/admin/shareable-users", headers=_owner_h())
    assert resp.status_code == 200, resp.text
    users = resp.json()["users"]
    assert users, "候选名单不应为空"
    ids = {u["id"] for u in users}
    usernames = {u["username"] for u in users}
    assert _user_id(_OWNER) not in ids
    assert _OWNER not in usernames
    assert _SHARER in usernames
    for u in users:
        assert set(u.keys()) == {"id", "username"}, f"泄露字段: {u.keys()}"


# ════════════════════════════════════════════
#  S2 非属主操作边界
# ════════════════════════════════════════════

def test_s2_shared_agent_mutations_forbidden():
    h = _sharer_h()
    assert client.put(f"/api/admin/agents/{_AGENT}", json={"name": "hacked"}, headers=h).status_code == 403
    assert client.post(f"/api/admin/agents/{_AGENT}/status", json={}, headers=h).status_code == 403
    assert client.post(f"/api/admin/agents/{_AGENT}/token", json={}, headers=h).status_code == 403
    assert client.post(f"/api/admin/agents/{_AGENT}/share", json={"shared_with": "all"}, headers=h).status_code == 403
    assert client.delete(f"/api/admin/agents/{_AGENT}", headers=h).status_code == 403
    assert client.put(f"/api/admin/agents/{_AGENT}/config", json={"ws_reconnect_interval": 9}, headers=h).status_code == 403
    assert client.post(f"/api/admin/agents/{_AGENT}/upgrade", json={}, headers=h).status_code == 403


def test_s2_shared_agent_config_viewable_but_shares_not():
    h = _sharer_h()
    # 可查看（只读）
    assert client.get(f"/api/admin/agents/{_AGENT}/config", headers=h).status_code == 200
    # 分享名单（属主管理信息）不可见
    assert client.get(f"/api/admin/agents/{_AGENT}/shares", headers=h).status_code == 403
    # 属主自己可以
    assert client.get(f"/api/admin/agents/{_AGENT}/shares", headers=_owner_h()).status_code == 200


# ════════════════════════════════════════════
#  S3 机密遮蔽
# ════════════════════════════════════════════

def test_s3_token_masked_for_shared_agent():
    owner_agents = client.get("/api/admin/agents", headers=_owner_h()).json()["agents"]
    own = [a for a in owner_agents if a["id"] == _AGENT][0]
    assert own["is_owner"] is True
    assert own["owner_name"] == ""
    assert own["token"], "属主应看到 token"

    sharer_agents = client.get("/api/admin/agents", headers=_sharer_h()).json()["agents"]
    shared = [a for a in sharer_agents if a["id"] == _AGENT][0]
    assert shared["is_owner"] is False
    assert shared["owner_name"] == _OWNER
    assert shared["token"] == "", "非属主不应拿到 token"


def test_s3_owner_actions_still_work():
    h = _owner_h()
    assert client.post(f"/api/admin/agents/{_AGENT}/share", json={"shared_with": ["someone"]}, headers=h).status_code == 200
    assert client.post(f"/api/admin/agents/{_AGENT}/share", json={"shared_with": [_user_id(_SHARER)]}, headers=h).status_code == 200


# ════════════════════════════════════════════
#  S4 权限自省
# ════════════════════════════════════════════

def test_s4_my_permissions_matches_matrix():
    resp = client.get("/api/admin/me/permissions", headers=_sharer_h())
    assert resp.status_code == 200, resp.text
    data = resp.json()
    assert data["role"] == "user"
    assert set(data["permissions"]) == set(ROLE_PERMISSIONS["user"])
    # 关键约束：普通角色不得含用户管理/审计/系统管理
    for forbidden in ("user:manage", "audit:read", "system:admin"):
        assert forbidden not in data["permissions"]
    # 前端菜单门禁依赖 agent:manage / coturn:manage 可用
    assert "agent:manage" in data["permissions"]
    assert "coturn:manage" in data["permissions"]


# ════════════════════════════════════════════
#  S5 共享 Gateway / Coturn（除 Agent 外同类资源）
# ════════════════════════════════════════════

_GW = f"test-share-gw-{_TS}"
_COTURN = ""  # coturn ID 由服务端生成，创建后回填
_COTURN_SECRET = "super-secret-hmac"


def _seed_gateway_and_coturn():
    """属主各建一个并共享给 sharer（幂等）"""
    h = _owner_h()
    sharer_id = _user_id(_SHARER)
    if not client.get("/api/admin/gateways", headers=h).json()["gateways"]:
        pass
    gws = [g["id"] for g in client.get("/api/admin/gateways", headers=h).json()["gateways"]]
    if _GW not in gws:
        client.post("/api/admin/gateways", json={"id": _GW, "name": "share-gw", "url": "wss://gw.test:9443", "remark": ""}, headers=h)
    global _COTURN
    for c in client.get("/api/admin/coturn", headers=h).json()["servers"]:
        if c.get("secret") == _COTURN_SECRET or c.get("host") == "cot.test":
            _COTURN = c["id"]
            break
    if not _COTURN:
        r = client.post("/api/admin/coturn", json={
            "name": "share-cot", "host": "cot.test", "port": 3478, "tls_port": 5349,
            "secret": _COTURN_SECRET, "realm": "pyterm.local",
            "relay_range": "49160-49259", "total_quota": 100, "remark": "",
        }, headers=h)
        assert r.status_code == 200, r.text
        _COTURN = r.json().get("id") or client.get("/api/admin/coturn", headers=h).json()["servers"][0]["id"]
    assert _COTURN
    assert client.post(f"/api/admin/gateways/{_GW}/share", json={"shared_with": [sharer_id]}, headers=h).status_code == 200
    assert client.post(f"/api/admin/coturn/{_COTURN}/share", json={"shared_with": [sharer_id]}, headers=h).status_code == 200


def test_s5_shared_gateway_mutation_forbidden_and_token_masked():
    _seed_gateway_and_coturn()
    h = _sharer_h()
    assert client.put(f"/api/admin/gateways/{_GW}", json={"name": "hacked"}, headers=h).status_code == 403
    assert client.post(f"/api/admin/gateways/{_GW}/status", json={}, headers=h).status_code == 403
    assert client.post(f"/api/admin/gateways/{_GW}/token", json={}, headers=h).status_code == 403
    assert client.post(f"/api/admin/gateways/{_GW}/share", json={"shared_with": "all"}, headers=h).status_code == 403
    assert client.delete(f"/api/admin/gateways/{_GW}", headers=h).status_code == 403
    assert client.post(f"/api/admin/gateways/{_GW}/upgrade", json={}, headers=h).status_code == 403
    assert client.get(f"/api/admin/gateways/{_GW}/shares", headers=h).status_code == 403
    assert client.get(f"/api/admin/gateways/{_GW}/shares", headers=_owner_h()).status_code == 200

    shared = [g for g in client.get("/api/admin/gateways", headers=h).json()["gateways"] if g["id"] == _GW][0]
    assert shared["is_owner"] is False
    assert shared["owner_name"] == _OWNER
    assert shared["token"] == "", "非属主不应拿到 gateway token"
    own = [g for g in client.get("/api/admin/gateways", headers=_owner_h()).json()["gateways"] if g["id"] == _GW][0]
    assert own["is_owner"] is True and own["token"]


def test_s5_shared_coturn_mutation_forbidden_and_secret_masked():
    _seed_gateway_and_coturn()
    h = _sharer_h()
    assert client.put(f"/api/admin/coturn/{_COTURN}", json={"name": "hacked"}, headers=h).status_code == 403
    assert client.delete(f"/api/admin/coturn/{_COTURN}", headers=h).status_code == 403
    assert client.post(f"/api/admin/coturn/{_COTURN}/share", json={"shared_with": "all"}, headers=h).status_code == 403
    assert client.post(f"/api/admin/coturn/{_COTURN}/default", json={}, headers=h).status_code == 403
    assert client.get(f"/api/admin/coturn/{_COTURN}/shares", headers=h).status_code == 403
    assert client.get(f"/api/admin/coturn/{_COTURN}/test-credentials", headers=h).status_code == 403
    assert client.get(f"/api/admin/coturn/{_COTURN}/test-credentials", headers=_owner_h()).status_code == 200
    assert client.get(f"/api/admin/coturn/{_COTURN}/shares", headers=_owner_h()).status_code == 200

    shared = [c for c in client.get("/api/admin/coturn", headers=h).json()["servers"] if c["id"] == _COTURN][0]
    assert shared["is_owner"] is False
    assert shared["owner_name"] == _OWNER
    assert shared["secret"] == "", "非属主不应拿到 coturn secret"
    own = [c for c in client.get("/api/admin/coturn", headers=_owner_h()).json()["servers"] if c["id"] == _COTURN][0]
    assert own["is_owner"] is True and own["secret"] == _COTURN_SECRET
