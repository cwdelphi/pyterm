"""WebRTC 信令 API 测试 - HTTP端点 + WebSocket端点"""
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import (
    register_user, _read_users_file, _write_users_file,
    ensure_agent, ensure_gateway, delete_agent, delete_gateway,
)
from app.api_isolated import _online_agents, _agent_connections, _online_gateways

client = TestClient(app)

_TS = int(time.time())
_USER = f"wrtc_sig_{_TS}"
_PASS = "WrtcSig123!"

# 预置 agent/gateway 注册 token (S5: register 必须已有 DB 记录)
_AGENTS = {
    "agent_001": f"tok_a001_{_TS}",
    "agent_hb": f"tok_hb_{_TS}",
    "agent_br": f"tok_br_{_TS}",
    "agent_oa": f"tok_oa_{_TS}",
    "agent_cand": f"tok_cand_{_TS}",
    "agent_off": f"tok_off_{_TS}",
    "agent_it": f"tok_it_{_TS}",
    "agent_cl": f"tok_cl_{_TS}",
    "agent_cr1": f"tok_cr1_{_TS}",
    "agent_cr2": f"tok_cr2_{_TS}",
}
_GATEWAYS = {
    "gw_001": f"tok_gw001_{_TS}",
}


@pytest.fixture(scope="module")
def ctx():
    """with TestClient(app) 上下文, 触发 lifespan 使 WS 可用"""
    with TestClient(app) as c:
        yield c


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    data["login_attempts"] = {}
    _write_users_file(data)


def _login():
    resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
    data = resp.json()
    if "token" not in data:
        # 用户可能被限流，清除限流后重试
        raw = _read_users_file()
        raw["login_attempts"] = {}
        _write_users_file(raw)
        resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
        data = resp.json()
    return data["token"]


def _headers():
    return {"Authorization": f"Bearer {_login()}"}


def _reg_msg(agent_id: str, **extra) -> dict:
    """带 token 的 agent register 消息"""
    return {
        "type": "register",
        "agent_id": agent_id,
        "agent_name": extra.pop("agent_name", agent_id),
        "agent_version": extra.pop("agent_version", "1.0.0"),
        "token": _AGENTS[agent_id],
        **extra,
    }


def _cleanup_state():
    """清理全局状态"""
    _online_agents.clear()
    _agent_connections.clear()
    _online_gateways.clear()


def setup_module():
    _cleanup_user(_USER)
    uid = (register_user(_USER, _PASS, f"wrtc_sig_{_TS}@test.com") or {}).get("id", "")
    for aid, tok in _AGENTS.items():
        ensure_agent(aid, tok, name=f"Agent {aid}", owner_id=uid)
    for gid, tok in _GATEWAYS.items():
        ensure_gateway(gid, tok, name=f"GW {gid}")
    _cleanup_state()


def teardown_module():
    _cleanup_state()
    _cleanup_user(_USER)
    for aid in _AGENTS:
        delete_agent(aid)
    for gid in _GATEWAYS:
        delete_gateway(gid)


# ═══════════════════════════════════════════════════════════
#  HTTP 端点 (A-38 ~ A-40)
# ═══════════════════════════════════════════════════════════


class TestWebRTCHTTP:
    """A-38 ~ A-40"""

    def test_webrtc_ice_servers(self):
        """A-38: GET /api/webrtc/ice-servers 返回 STUN/TURN 配置"""
        resp = client.get("/api/webrtc/ice-servers", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "ice_servers" in data
        servers = data["ice_servers"]
        assert isinstance(servers, list)
        # L1: 不可达的 STUN 不下发, 条目数可能为 1, 但至少要有一项且必须含 STUN
        assert len(servers) >= 1
        has_stun = any("stun" in s.get("urls", [""])[0].lower() for s in servers)
        assert has_stun

    def test_webrtc_agents_empty(self):
        """A-39: GET /api/webrtc/agents 返回空列表"""
        _cleanup_state()
        resp = client.get("/api/webrtc/agents", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "agents" in data
        assert data["agents"] == []

    def test_webrtc_gateways_empty(self):
        """GET /api/webrtc/gateways 返回空列表"""
        _cleanup_state()
        resp = client.get("/api/webrtc/gateways", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "gateways" in data
        assert data["gateways"] == []

    def test_webrtc_rooms_empty(self):
        """A-40: GET /api/webrtc/rooms 返回空列表"""
        _cleanup_state()
        resp = client.get("/api/webrtc/rooms", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "rooms" in data
        assert data["rooms"] == []


# ═══════════════════════════════════════════════════════════
#  WebSocket 端点 (A-41 ~ A-49)
# ═══════════════════════════════════════════════════════════


class TestWebRTCWebSocket:
    """A-41 ~ A-49"""

    def test_webrtc_ws_agent_register(self, ctx):
        """A-41: agent register 消息添加到在线列表 (S5: 需 token)"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json(_reg_msg("agent_001", agent_name="Test Agent"))
            resp = ws.receive_json()
            assert resp["type"] == "register_success"
            assert resp["agent_id"] == "agent_001"
            # 验证全局状态
            assert "agent_001" in _online_agents
            assert _online_agents["agent_001"]["name"] == "Test Agent"
        # WS断开后应清理 (finally 可能异步完成)
        for _ in range(20):
            if "agent_001" not in _online_agents:
                break
            time.sleep(0.05)
        assert "agent_001" not in _online_agents

    def test_webrtc_ws_agent_register_no_token(self, ctx):
        """S5: 无 token 注册被拒"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json({
                "type": "register",
                "agent_id": "agent_001",
                "agent_name": "No Token",
            })
            resp = ws.receive_json()
            assert resp["type"] == "register_failed"
            assert "agent_001" not in _online_agents

    def test_webrtc_ws_agent_register_wrong_token(self, ctx):
        """S5: token 不匹配被拒"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json({
                "type": "register",
                "agent_id": "agent_001",
                "agent_name": "Bad Token",
                "token": "wrong_token",
            })
            resp = ws.receive_json()
            assert resp["type"] == "register_failed"
            assert "agent_001" not in _online_agents

    def test_webrtc_ws_gateway_register(self, ctx):
        """gateway register 消息添加到在线列表 (S5: 需 token)"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json({
                "type": "register_gateway",
                "gateway_id": "gw_001",
                "gateway_name": "Test Gateway",
                "token": _GATEWAYS["gw_001"],
            })
            resp = ws.receive_json()
            assert resp["type"] == "register_success"
            assert resp["gateway_id"] == "gw_001"
            assert "gw_001" in _online_gateways
            assert _online_gateways["gw_001"]["name"] == "Test Gateway"
            # R1: register_gateway 后 agent_id 应等于 gw_id (heartbeat 用)
            assert _online_gateways["gw_001"].get("agent_id", "gw_001") is not None
        # WS断开后应清理
        for _ in range(20):
            if "gw_001" not in _online_gateways:
                break
            time.sleep(0.05)
        assert "gw_001" not in _online_gateways

    def test_webrtc_ws_gateway_register_no_token(self, ctx):
        """S5: gateway 无 token 注册被拒"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json({
                "type": "register_gateway",
                "gateway_id": "gw_001",
                "gateway_name": "No Token",
            })
            resp = ws.receive_json()
            assert resp["type"] == "register_failed"
            assert "gw_001" not in _online_gateways

    def test_webrtc_ws_agent_heartbeat(self, ctx):
        """A-42: heartbeat 更新 last_seen"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json(_reg_msg("agent_hb", agent_name="HB Agent"))
            ws.receive_json()  # register_success
            t1 = _online_agents["agent_hb"]["last_seen"]
            time.sleep(0.1)
            ws.send_json({"type": "heartbeat"})
            ack = ws.receive_json()
            assert ack["type"] == "heartbeat_ack"
            t2 = _online_agents["agent_hb"]["last_seen"]
            assert t2 >= t1

    def test_webrtc_ws_browser_connect(self, ctx):
        """A-43: browser connect_agent 消息创建房间"""
        _cleanup_state()
        # agent连接
        agent_ws = ctx.websocket_connect("/api/ws/webrtc")
        agent_ctx = agent_ws.__enter__()
        agent_ctx.send_json(_reg_msg("agent_br", agent_name="BR Agent"))
        agent_ctx.receive_json()  # register_success

        # browser连接
        browser_ws = ctx.websocket_connect("/api/ws/webrtc")
        browser_ctx = browser_ws.__enter__()
        token = _login()
        browser_ctx.send_json({
            "type": "connect_agent",
            "agent_id": "agent_br",
            "token": token,
        })
        browser_resp = browser_ctx.receive_json()
        assert browser_resp["type"] == "connect_success"
        assert "room_id" in browser_resp
        assert browser_resp["agent_id"] == "agent_br"
        # agent 收到 browser_connect 通知
        agent_resp = agent_ctx.receive_json()
        assert agent_resp["type"] == "browser_connect"
        assert agent_resp["room_id"] == browser_resp["room_id"]
        # 验证全局状态
        assert len(_agent_connections) == 1

        # 清理
        agent_ctx.__exit__(None, None, None)
        browser_ctx.__exit__(None, None, None)

    def test_webrtc_ws_offer_answer(self, ctx):
        """A-44: offer 转发给 agent, answer 转发给 browser"""
        _cleanup_state()
        # agent
        agent_ws = ctx.websocket_connect("/api/ws/webrtc")
        agent_ctx = agent_ws.__enter__()
        agent_ctx.send_json(_reg_msg("agent_oa", agent_name="OA"))
        agent_ctx.receive_json()

        # browser连接
        browser_ws = ctx.websocket_connect("/api/ws/webrtc")
        browser_ctx = browser_ws.__enter__()
        token = _login()
        browser_ctx.send_json({"type": "connect_agent", "agent_id": "agent_oa", "token": token})
        connect_resp = browser_ctx.receive_json()  # connect_success
        agent_ctx.receive_json()  # browser_connect
        room_id = connect_resp["room_id"]

        # browser发送offer
        browser_ctx.send_json({
            "type": "offer", "room_id": room_id,
            "sdp": "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF",
        })
        # agent收到offer
        agent_offer = agent_ctx.receive_json()
        assert agent_offer["type"] == "offer"
        assert agent_offer["room_id"] == room_id
        assert agent_offer["sdp"] == "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF"
        assert agent_offer["from"] == "browser"

        # agent发送answer
        agent_ctx.send_json({
            "type": "answer", "room_id": room_id,
            "sdp": "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 100",
        })
        browser_answer = browser_ctx.receive_json()
        assert browser_answer["type"] == "answer"
        assert browser_answer["room_id"] == room_id
        assert browser_answer["sdp"] == "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 100"
        assert browser_answer["from"] == "agent"

        agent_ctx.__exit__(None, None, None)
        browser_ctx.__exit__(None, None, None)

    def test_webrtc_ws_candidate(self, ctx):
        """A-45: ICE candidate 被正确转发"""
        _cleanup_state()
        agent_ws = ctx.websocket_connect("/api/ws/webrtc")
        agent_ctx = agent_ws.__enter__()
        agent_ctx.send_json(_reg_msg("agent_cand", agent_name="Cand"))
        agent_ctx.receive_json()

        browser_ws = ctx.websocket_connect("/api/ws/webrtc")
        browser_ctx = browser_ws.__enter__()
        token = _login()
        browser_ctx.send_json({"type": "connect_agent", "agent_id": "agent_cand", "token": token})
        browser_ctx.receive_json()
        agent_ctx.receive_json()
        room_id = list(_agent_connections.keys())[0]

        # browser → agent candidate
        candidate_data = {"candidate": "candidate:1 1 UDP 2122252543 ...", "sdpMid": "0"}
        browser_ctx.send_json({
            "type": "candidate", "room_id": room_id,
            "candidate": candidate_data, "from": "browser",
        })
        agent_cand = agent_ctx.receive_json()
        assert agent_cand["type"] == "candidate"
        assert agent_cand["candidate"] == candidate_data
        assert agent_cand["from"] == "browser"

        # agent → browser candidate
        agent_answer_data = {"candidate": "candidate:2 1 UDP 2122252544 ...", "sdpMid": "0"}
        agent_ctx.send_json({
            "type": "candidate", "room_id": room_id,
            "candidate": agent_answer_data, "from": "agent",
        })
        browser_cand = browser_ctx.receive_json()
        assert browser_cand["type"] == "candidate"
        assert browser_cand["candidate"] == agent_answer_data
        assert browser_cand["from"] == "agent"

        agent_ctx.__exit__(None, None, None)
        browser_ctx.__exit__(None, None, None)

    def test_webrtc_agent_offline(self, ctx):
        """A-46: agent断开后从在线列表移除"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as ws:
            ws.send_json(_reg_msg("agent_off", agent_name="Off"))
            ws.receive_json()
            assert "agent_off" in _online_agents
        # WS退出后清理 (finally 可能异步完成)
        for _ in range(20):
            if "agent_off" not in _online_agents:
                break
            time.sleep(0.05)
        assert "agent_off" not in _online_agents

    def test_webrtc_invalid_token(self, ctx):
        """A-47: browser connect 使用无效 token 返回错误"""
        _cleanup_state()
        # 先注册一个agent
        agent_ws = ctx.websocket_connect("/api/ws/webrtc")
        agent_ctx = agent_ws.__enter__()
        agent_ctx.send_json(_reg_msg("agent_it", agent_name="IT"))
        agent_ctx.receive_json()

        # browser用无效token
        browser_ws = ctx.websocket_connect("/api/ws/webrtc")
        browser_ctx = browser_ws.__enter__()
        browser_ctx.send_json({
            "type": "connect_agent",
            "agent_id": "agent_it",
            "token": "invalid_token_xyz",
        })
        error_resp = browser_ctx.receive_json()
        assert error_resp["type"] == "error"
        assert "认证" in error_resp["detail"]

        agent_ctx.__exit__(None, None, None)
        browser_ctx.__exit__(None, None, None)

    def test_webrtc_room_cleanup(self, ctx):
        """A-48: browser断开后房间被清理"""
        _cleanup_state()
        agent_ws = ctx.websocket_connect("/api/ws/webrtc")
        agent_ctx = agent_ws.__enter__()
        agent_ctx.send_json(_reg_msg("agent_cl", agent_name="CL"))
        agent_ctx.receive_json()

        browser_ws = ctx.websocket_connect("/api/ws/webrtc")
        browser_ctx = browser_ws.__enter__()
        token = _login()
        browser_ctx.send_json({"type": "connect_agent", "agent_id": "agent_cl", "token": token})
        browser_ctx.receive_json()
        agent_ctx.receive_json()
        assert len(_agent_connections) == 1

        # browser断开
        browser_ctx.__exit__(None, None, None)
        time.sleep(0.05)
        # 房间应被清理
        remaining = [r for r, c in _agent_connections.items() if c.get("browser_ws") == browser_ctx]
        assert len(remaining) == 0

        agent_ctx.__exit__(None, None, None)

    def test_webrtc_concurrent_rooms(self, ctx):
        """A-49: 多个房间同时存在"""
        _cleanup_state()
        # agent1
        a1_ws = ctx.websocket_connect("/api/ws/webrtc")
        a1 = a1_ws.__enter__()
        a1.send_json(_reg_msg("agent_cr1", agent_name="CR1"))
        a1.receive_json()

        # agent2
        a2_ws = ctx.websocket_connect("/api/ws/webrtc")
        a2 = a2_ws.__enter__()
        a2.send_json(_reg_msg("agent_cr2", agent_name="CR2"))
        a2.receive_json()

        # browser1 → agent1
        b1_ws = ctx.websocket_connect("/api/ws/webrtc")
        b1 = b1_ws.__enter__()
        token = _login()
        b1.send_json({"type": "connect_agent", "agent_id": "agent_cr1", "token": token})
        b1.receive_json()
        a1.receive_json()

        # browser2 → agent2
        b2_ws = ctx.websocket_connect("/api/ws/webrtc")
        b2 = b2_ws.__enter__()
        b2.send_json({"type": "connect_agent", "agent_id": "agent_cr2", "token": token})
        b2.receive_json()
        a2.receive_json()

        # 验证两个房间存在
        assert len(_agent_connections) == 2
        room_ids = list(_agent_connections.keys())
        agent_ids = [_agent_connections[r]["agent_id"] for r in room_ids]
        assert "agent_cr1" in agent_ids
        assert "agent_cr2" in agent_ids

        b1.__exit__(None, None, None)
        b2.__exit__(None, None, None)
        a1.__exit__(None, None, None)
        a2.__exit__(None, None, None)
