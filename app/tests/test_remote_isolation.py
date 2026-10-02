"""远程管理隔离测试(批次A) — S1/S2/S3/S4/S6

覆盖:
- S1  connect_agent / connect_gateway 可见性: 非属主且未共享 → error 且不建房
- S2  GET /api/webrtc/agents、/api/webrtc/gateways 仅返回当前用户可见项
- S3  GET /api/webrtc/rooms 仅返回本人房间(他人/tunnel 房间不外泄)
- S4  GET /api/logs 需 system:admin(匿名 401 / 普通用户 403)；POST /api/log 保持开放(登录前上报)
- S6  POST /api/webrtc/webterm-open 对不可见 Agent → 403
"""
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import (
    register_user, delete_user_by_username,
    ensure_agent, ensure_gateway, delete_agent, delete_gateway,
    _read_users_file, _write_users_file,
)
from app.api_isolated import _online_agents, _agent_connections, _online_gateways

client = TestClient(app)

_TS = int(time.time())
_OWNER = f"iso_owner_{_TS}"
_INTR = f"iso_intr_{_TS}"
_ADMIN = f"iso_admin_{_TS}"
_PASS = "IsoTest123!"

_AG_PRIV = f"iso-agent-priv-{_TS}"    # 仅属主可见
_AG_SHARED = f"iso-agent-shr-{_TS}"   # 属主共享给闯入者
_GW = f"iso-gw-{_TS}"
_TOK_PRIV = f"tok_priv_{_TS}"
_TOK_SHARED = f"tok_shr_{_TS}"
_TOK_GW = f"tok_gw_{_TS}"

_TOKENS: dict = {}


@pytest.fixture(scope="module")
def ctx():
    """with TestClient(app) 上下文, 触发 lifespan 使 WS 可用"""
    with TestClient(app) as c:
        yield c


def _cleanup_user(username: str):
    delete_user_by_username(username)
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    data["login_attempts"] = {}
    _write_users_file(data)


def _login(username: str) -> str:
    """返回 JWT(缓存, 避免重复打登录限流)"""
    if username in _TOKENS:
        return _TOKENS[username]
    resp = client.post("/api/auth/login", json={"username": username, "password": _PASS})
    assert resp.status_code == 200, resp.text
    tok = resp.json().get("token", "")
    assert tok, resp.json()
    _TOKENS[username] = tok
    return tok


def _headers(username: str) -> dict:
    return {"Authorization": f"Bearer {_login(username)}"}


def _reg_msg(agent_id: str, token: str) -> dict:
    return {
        "type": "register",
        "agent_id": agent_id,
        "agent_name": agent_id,
        "agent_version": "1.0.0",
        "token": token,
    }


def _cleanup_state():
    _online_agents.clear()
    _agent_connections.clear()
    _online_gateways.clear()


def setup_module():
    _TOKENS.clear()
    for u in (_OWNER, _INTR, _ADMIN):
        _cleanup_user(u)
    owner_id = (register_user(_OWNER, _PASS, f"{_OWNER}@test.com") or {}).get("id", "")
    intr_id = (register_user(_INTR, _PASS, f"{_INTR}@test.com") or {}).get("id", "")
    register_user(_ADMIN, _PASS, f"{_ADMIN}@test.com", role="admin")

    ensure_agent(_AG_PRIV, _TOK_PRIV, name="Isolation Private", owner_id=owner_id)
    ensure_agent(_AG_SHARED, _TOK_SHARED, name="Isolation Shared", owner_id=owner_id)
    ensure_gateway(_GW, _TOK_GW, name="Isolation GW", owner_id=owner_id)
    # 属主把 _AG_SHARED 共享给闯入者(正向对照: 共享来的必须可见)
    r = client.post(f"/api/admin/agents/{_AG_SHARED}/share",
                    json={"shared_with": [intr_id]}, headers=_headers(_OWNER))
    assert r.status_code == 200, r.text
    _cleanup_state()


def teardown_module():
    _cleanup_state()
    _TOKENS.clear()
    for aid in (_AG_PRIV, _AG_SHARED):
        delete_agent(aid)
    delete_gateway(_GW)
    for u in (_OWNER, _INTR, _ADMIN):
        _cleanup_user(u)


# ════════════════════════════════════════════════════════════
#  S1 信令建房可见性
# ════════════════════════════════════════════════════════════

class TestS1ConnectVisibility:
    def test_connect_agent_denied_for_stranger(self, ctx):
        """非属主且未共享 → error, 且不创建房间"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ag:
            ag.send_json(_reg_msg(_AG_PRIV, _TOK_PRIV))
            assert ag.receive_json()["type"] == "register_success"
            with ctx.websocket_connect("/api/ws/webrtc") as br:
                br.send_json({"type": "connect_agent", "agent_id": _AG_PRIV,
                              "token": _login(_INTR)})
                resp = br.receive_json()
                assert resp["type"] == "error", resp
                assert "无权" in resp["detail"] or "permission" in resp["detail"].lower()
                assert _agent_connections == {}

    def test_connect_agent_allowed_for_owner(self, ctx):
        """属主 → 正常建房"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ag:
            ag.send_json(_reg_msg(_AG_PRIV, _TOK_PRIV))
            assert ag.receive_json()["type"] == "register_success"
            with ctx.websocket_connect("/api/ws/webrtc") as br:
                br.send_json({"type": "connect_agent", "agent_id": _AG_PRIV,
                              "token": _login(_OWNER)})
                resp = br.receive_json()
                assert resp["type"] == "connect_success", resp
                assert resp["agent_id"] == _AG_PRIV
                assert len(_agent_connections) == 1
        _cleanup_state()

    def test_connect_agent_allowed_for_shared_user(self, ctx):
        """共享来的用户 → 同样可建房(收紧不能误伤共享链路)"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ag:
            ag.send_json(_reg_msg(_AG_SHARED, _TOK_SHARED))
            assert ag.receive_json()["type"] == "register_success"
            with ctx.websocket_connect("/api/ws/webrtc") as br:
                br.send_json({"type": "connect_agent", "agent_id": _AG_SHARED,
                              "token": _login(_INTR)})
                resp = br.receive_json()
                assert resp["type"] == "connect_success", resp
                assert len(_agent_connections) == 1
        _cleanup_state()

    def test_connect_gateway_denied_for_stranger(self, ctx):
        """网关未共享 → error 且不建房"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as gw:
            gw.send_json({
                "type": "register_gateway",
                "gateway_id": _GW,
                "gateway_name": _GW,
                "token": _TOK_GW,
            })
            assert gw.receive_json()["type"] == "register_success"
            assert _GW in _online_gateways
            with ctx.websocket_connect("/api/ws/webrtc") as br:
                br.send_json({"type": "connect_gateway", "gateway_id": _GW,
                              "token": _login(_INTR)})
                resp = br.receive_json()
                assert resp["type"] == "error", resp
                assert "无权" in resp["detail"] or "permission" in resp["detail"].lower()
                assert _agent_connections == {}
        _cleanup_state()


# ════════════════════════════════════════════════════════════
#  S2 列表可见性
# ════════════════════════════════════════════════════════════

class TestS2ListFilter:
    def test_agents_list_filtered(self, ctx):
        """在线列表按可见性过滤: 属主两个都在, 闯入者只看到共享的那个"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                ctx.websocket_connect("/api/ws/webrtc") as a2:
            a1.send_json(_reg_msg(_AG_PRIV, _TOK_PRIV))
            assert a1.receive_json()["type"] == "register_success"
            a2.send_json(_reg_msg(_AG_SHARED, _TOK_SHARED))
            assert a2.receive_json()["type"] == "register_success"

            owner_ids = {a["id"] for a in
                         client.get("/api/webrtc/agents", headers=_headers(_OWNER)).json()["agents"]}
            intr_ids = {a["id"] for a in
                        client.get("/api/webrtc/agents", headers=_headers(_INTR)).json()["agents"]}

            assert _AG_PRIV in owner_ids and _AG_SHARED in owner_ids
            assert _AG_SHARED in intr_ids
            assert _AG_PRIV not in intr_ids
        _cleanup_state()

    def test_gateways_list_filtered(self, ctx):
        """网关列表: 属主可见, 未共享的闯入者不可见"""
        _cleanup_state()
        _online_gateways[_GW] = {
            "ws": None, "name": _GW, "version": "1.0.0", "status": "online",
            "ip": "-", "last_seen": time.time(),
        }
        owner_ids = {g["id"] for g in
                     client.get("/api/webrtc/gateways", headers=_headers(_OWNER)).json()["gateways"]}
        intr_ids = {g["id"] for g in
                    client.get("/api/webrtc/gateways", headers=_headers(_INTR)).json()["gateways"]}
        assert _GW in owner_ids
        assert _GW not in intr_ids
        _cleanup_state()


# ════════════════════════════════════════════════════════════
#  S3 房间可见性
# ════════════════════════════════════════════════════════════

class TestS3RoomsFilter:
    def test_rooms_only_own(self, ctx):
        """属主建房后: 属主能查到, 闯入者查不到"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ag:
            ag.send_json(_reg_msg(_AG_PRIV, _TOK_PRIV))
            assert ag.receive_json()["type"] == "register_success"
            with ctx.websocket_connect("/api/ws/webrtc") as br:
                br.send_json({"type": "connect_agent", "agent_id": _AG_PRIV,
                              "token": _login(_OWNER)})
                assert br.receive_json()["type"] == "connect_success"

                owner_rooms = client.get(
                    "/api/webrtc/rooms", headers=_headers(_OWNER)).json()["rooms"]
                intr_rooms = client.get(
                    "/api/webrtc/rooms", headers=_headers(_INTR)).json()["rooms"]
                assert len(owner_rooms) == 1
                assert owner_rooms[0]["agent_id"] == _AG_PRIV
                assert intr_rooms == []
        _cleanup_state()


# ════════════════════════════════════════════════════════════
#  S4 日志端点鉴权
# ════════════════════════════════════════════════════════════

class TestS4LogsAuth:
    def test_logs_requires_admin(self, ctx):
        """匿名 401 / 普通用户 403 / admin 200"""
        assert client.get("/api/logs").status_code == 401
        assert client.get("/api/logs", headers=_headers(_INTR)).status_code == 403
        r = client.get("/api/logs", headers=_headers(_ADMIN))
        assert r.status_code == 200
        assert "lines" in r.json()

    def test_post_log_stays_open(self, ctx):
        """登录前异常也要能上报 → 匿名 POST /api/log 仍 200"""
        r = client.post("/api/log", json={"level": "error", "message": "iso-test", "url": "/t"})
        assert r.status_code == 200
        assert r.json().get("ok") is True


# ════════════════════════════════════════════════════════════
#  S6 webterm-open 审计归属
# ════════════════════════════════════════════════════════════

class TestS6WebtermAudit:
    def test_webterm_open_visibility(self, ctx):
        """不可见 Agent → 403；属主/共享用户 → 200"""
        body_priv = {"agent_id": _AG_PRIV, "agent_name": "iso"}
        body_shr = {"agent_id": _AG_SHARED, "agent_name": "iso"}

        assert client.post("/api/webrtc/webterm-open", json=body_priv,
                           headers=_headers(_INTR)).status_code == 403
        assert client.post("/api/webrtc/webterm-open", json=body_priv,
                           headers=_headers(_OWNER)).status_code == 200
        assert client.post("/api/webrtc/webterm-open", json=body_shr,
                           headers=_headers(_INTR)).status_code == 200
