"""WebRTC信令API测试 - 4个用例"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, ensure_agent, delete_agent

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_WRTC_USER = f"wrtc_test_{int(_IMPORT_TS)}"
_WRTC_PASS = "Wrtc123!"
_TEST_AGENT_ID = "test_agent_001"
_TEST_AGENT_TOKEN = f"tok_api_{int(_IMPORT_TS)}"


def _cleanup_user(username: str):
    from app.tests.sync_db import _read_users_file, _write_users_file
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_WRTC_USER)
    uid = (register_user(_WRTC_USER, _WRTC_PASS, "wrtc@test.com") or {}).get("id", "")
    # S5: register 必须已有 DB 记录且 token 匹配
    # S6: webterm-open 审计需可见性, 测试 Agent 归属到测试用户
    ensure_agent(_TEST_AGENT_ID, _TEST_AGENT_TOKEN, name="API Test Agent", owner_id=uid)


def teardown_module():
    _cleanup_user(_WRTC_USER)
    delete_agent(_TEST_AGENT_ID)


def _get_token():
    resp = client.post("/api/auth/login", json={
        "username": _WRTC_USER, "password": _WRTC_PASS
    })
    return resp.json()["token"]


def _auth_headers():
    return {"Authorization": f"Bearer {_get_token()}"}


class TestICEServers:
    def test_get_ice_servers(self):
        """获取ICE服务器配置"""
        resp = client.get("/api/webrtc/ice-servers", headers=_auth_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "ice_servers" in data
        assert isinstance(data["ice_servers"], list)
        assert len(data["ice_servers"]) > 0
        # 验证ICE服务器结构
        server = data["ice_servers"][0]
        assert "urls" in server


class TestAgentList:
    def test_get_online_agents(self):
        """获取在线Agent列表"""
        resp = client.get("/api/webrtc/agents", headers=_auth_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "agents" in data
        assert isinstance(data["agents"], list)

    def test_get_active_rooms(self):
        """获取活跃房间列表"""
        resp = client.get("/api/webrtc/rooms", headers=_auth_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "rooms" in data
        assert isinstance(data["rooms"], list)


class TestWebRTCSignaling:
    def test_webrtc_websocket_connection(self):
        """WebRTC信令WebSocket连接 (S5: 需 token)"""
        with TestClient(app) as ctx:
            with ctx.websocket_connect("/api/ws/webrtc") as ws:
                # 发送注册消息
                ws.send_json({
                    "type": "register",
                    "agent_id": _TEST_AGENT_ID,
                    "agent_name": "test_agent",
                    "agent_version": "1.0.0",
                    "token": _TEST_AGENT_TOKEN,
                })
                # 接收注册成功响应
                resp = ws.receive_json()
                assert resp["type"] == "register_success"
                assert resp["agent_id"] == _TEST_AGENT_ID

    def test_webrtc_websocket_register_no_token(self):
        """S5: 无 token 注册被拒"""
        with TestClient(app) as ctx:
            with ctx.websocket_connect("/api/ws/webrtc") as ws:
                ws.send_json({
                    "type": "register",
                    "agent_id": _TEST_AGENT_ID,
                    "agent_name": "no_token",
                })
                resp = ws.receive_json()
                assert resp["type"] == "register_failed"


class TestWebtermAudit:
    def test_webterm_open_audit(self):
        """TC-W06: webterm 控制台打开留痕(N6)"""
        resp = client.post(
            "/api/webrtc/webterm-open",
            json={"agent_id": _TEST_AGENT_ID, "agent_name": "API Test Agent"},
            headers=_auth_headers(),
        )
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        from sqlalchemy import select
        from app.database import AuditLog
        from app.tests.sync_db import _make_sync_factories
        engine, Session = _make_sync_factories()
        with Session() as s:
            row = s.execute(
                select(AuditLog)
                .where(AuditLog.action == "agent.webterm_open",
                       AuditLog.target_id == _TEST_AGENT_ID)
                .order_by(AuditLog.id.desc())
            ).scalars().first()
        engine.dispose()
        assert row is not None
        assert row.username == _WRTC_USER
        assert row.target_type == "agent"

    def test_webterm_open_no_token(self):
        """TC-W06: 未认证访问被拒"""
        resp = client.post(
            "/api/webrtc/webterm-open",
            json={"agent_id": _TEST_AGENT_ID, "agent_name": "x"},
        )
        assert resp.status_code in (401, 403)
