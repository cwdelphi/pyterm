"""Agent↔Agent 测速 API/WS 测试(阶段S) — TC-ST02~07"""
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app import api_isolated
from app.api_isolated import (
    _online_agents, _agent_connections, _speedtest_history,
    SPEEDTEST_LIMITS,
)
from app.tests.sync_db import (
    register_user, _read_users_file, _write_users_file,
    ensure_agent, delete_agent,
)

client = TestClient(app)

_TS = int(time.time())
_USER = f"speedtest_{_TS}"
_PASS = "SpeedTest123!"

_AGENTS = {
    "st_src": f"tok_stsrc_{_TS}",
    "st_dst": f"tok_stdst_{_TS}",
}


@pytest.fixture(scope="module")
def ctx():
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
        raw = _read_users_file()
        raw["login_attempts"] = {}
        _write_users_file(raw)
        resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
        data = resp.json()
    return data["token"]


def _headers():
    return {"Authorization": f"Bearer {_login()}"}


def _set_owner(agent_id: str, owner_id: str):
    from app.database import Agent
    from app.tests.sync_db import _make_sync_factories
    engine, Session = _make_sync_factories()
    with Session() as s:
        row = s.execute(__import__("sqlalchemy").select(Agent).where(Agent.id == agent_id)).scalar_one_or_none()
        if row:
            row.owner_id = owner_id
            s.commit()
    engine.dispose()


def _reg_msg(agent_id: str) -> dict:
    return {
        "type": "register",
        "agent_id": agent_id,
        "agent_name": agent_id,
        "agent_version": "2.3.0",
        "token": _AGENTS[agent_id],
    }


def _cleanup_state():
    _online_agents.clear()
    _agent_connections.clear()
    _speedtest_history.clear()
    if api_isolated._speedtest_active is not None:
        task = api_isolated._speedtest_active.get("task")
        if task is not None:
            task.cancel()
        api_isolated._speedtest_active = None


def setup_module():
    _cleanup_user(_USER)
    u = register_user(_USER, _PASS, f"{_USER}@test.com")
    uid = u.get("id") or ""
    for aid, tok in _AGENTS.items():
        ensure_agent(aid, tok, name=f"Agent {aid}")
        if uid:
            _set_owner(aid, uid)
    _cleanup_state()


def teardown_module():
    _cleanup_state()
    _cleanup_user(_USER)
    for aid in _AGENTS:
        delete_agent(aid)


def _start(browser, src="st_src", dst="st_dst", limit=10):
    browser.send_json({
        "type": "speedtest_start", "source": src, "target": dst,
        "mbps_limit": limit, "token": _login(),
    })
    return browser.receive_json()


class TestSpeedtestWS:
    def test_st04_target_offline(self, ctx):
        """TC-ST04: 目标 Agent 离线 → 明确报错, 不建房间"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as agent_ctx:
            agent_ctx.send_json(_reg_msg("st_src"))
            agent_ctx.receive_json()  # register_success
            with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                resp = _start(browser_ctx, dst="st_offline_never")
                assert resp["type"] == "speedtest_error"
                assert "不在线" in resp["detail"] or "offline" in resp["detail"]
                assert len(_agent_connections) == 0
                assert api_isolated._speedtest_active is None

    def test_st05_no_turn(self, ctx, monkeypatch):
        """TC-ST05: 无 TURN 环境 → 明确报错「需配置Coturn」"""
        _cleanup_state()

        async def _no_turn():
            return False

        monkeypatch.setattr(api_isolated, "_has_active_turn", _no_turn)
        with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                ctx.websocket_connect("/api/ws/webrtc") as a2:
            a1.send_json(_reg_msg("st_src")); a1.receive_json()
            a2.send_json(_reg_msg("st_dst")); a2.receive_json()
            with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                resp = _start(browser_ctx)
                assert resp["type"] == "speedtest_error"
                assert "Coturn" in resp["detail"]
                assert len(_agent_connections) == 0

    def test_permission_denied(self, ctx):
        """目标 Agent 属于他人 → 拒绝(可见性过滤)"""
        _cleanup_state()
        # 临时用户 B 拥有 st_dst → 测试用户 A 看不到
        _cleanup_user(f"{_USER}_other")
        u2 = register_user(f"{_USER}_other", _PASS, "x@x.com")
        _set_owner("st_dst", u2.get("id", ""))
        try:
            with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                    ctx.websocket_connect("/api/ws/webrtc") as a2:
                a1.send_json(_reg_msg("st_src")); a1.receive_json()
                a2.send_json(_reg_msg("st_dst")); a2.receive_json()
                with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                    resp = _start(browser_ctx)
                    assert resp["type"] == "speedtest_error"
                    assert len(_agent_connections) == 0
        finally:
            _set_owner("st_dst", register_user(_USER, _PASS, f"{_USER}@test.com").get("id", ""))
            _cleanup_user(f"{_USER}_other")

    def test_invalid_limit_and_same_agent(self, ctx):
        """非法限速档 / 源=目标 → 明确报错"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                ctx.websocket_connect("/api/ws/webrtc") as a2:
            a1.send_json(_reg_msg("st_src")); a1.receive_json()
            a2.send_json(_reg_msg("st_dst")); a2.receive_json()
            with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                resp = _start(browser_ctx, limit=33)
                assert resp["type"] == "speedtest_error"
                resp = _start(browser_ctx, src="st_src", dst="st_src")
                assert resp["type"] == "speedtest_error"

    def test_st03_busy_then_st02_cancel(self, ctx):
        """TC-ST03 并发第二次发起返回 busy; TC-ST02 cancel 双端 stop+清房间"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                ctx.websocket_connect("/api/ws/webrtc") as a2:
            a1.send_json(_reg_msg("st_src")); a1.receive_json()
            a2.send_json(_reg_msg("st_dst")); a2.receive_json()
            with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                started = _start(browser_ctx)
                assert started["type"] == "speedtest_started"
                room = started["room_id"]
                # 两端通知
                conn_msg = a1.receive_json()
                assert conn_msg["type"] == "speedtest_connect"
                assert conn_msg["room_id"] == room
                assert conn_msg["duration"] == 10
                assert conn_msg["mbps_limit"] == 10
                notify = a2.receive_json()
                assert notify["type"] == "browser_connect"
                assert notify["room_id"] == room
                # TC-ST03: busy
                busy = _start(browser_ctx)
                assert busy["type"] == "speedtest_error"
                assert room in _agent_connections
                # TC-ST02: cancel → 双端 stop + 房间清理 + 锁释放
                browser_ctx.send_json({"type": "speedtest_cancel", "room_id": room})
                stop1 = a1.receive_json()
                stop2 = a2.receive_json()
                assert stop1["type"] == "speedtest_stop" and stop1["room_id"] == room
                assert stop2["type"] == "speedtest_stop" and stop2["room_id"] == room
                ack = browser_ctx.receive_json()
                assert ack["type"] == "speedtest_cancelled"
                assert room not in _agent_connections
                assert api_isolated._speedtest_active is None
                # 锁已释放: 可再次发起
                again = _start(browser_ctx)
                assert again["type"] == "speedtest_started"
                browser_ctx.send_json({"type": "speedtest_cancel", "room_id": again["room_id"]})
                a1.receive_json(); a2.receive_json(); browser_ctx.receive_json()

    def test_progress_and_result_forward(self, ctx):
        """进度/结果转发 + 结果入历史 + 会话自动结束"""
        _cleanup_state()
        with ctx.websocket_connect("/api/ws/webrtc") as a1, \
                ctx.websocket_connect("/api/ws/webrtc") as a2:
            a1.send_json(_reg_msg("st_src")); a1.receive_json()
            a2.send_json(_reg_msg("st_dst")); a2.receive_json()
            with ctx.websocket_connect("/api/ws/webrtc") as browser_ctx:
                started = _start(browser_ctx)
                assert started["type"] == "speedtest_started"
                room = started["room_id"]
                a1.receive_json()  # speedtest_connect
                a2.receive_json()  # browser_connect

                # 目标 agent 上报进度 → 浏览器
                a2.send_json({
                    "type": "speedtest_progress", "room_id": room,
                    "data": {"type": "speedtest_progress", "room_id": room,
                             "phase": "up", "mbps": 9.8, "bytes": 12345678},
                })
                prog = browser_ctx.receive_json()
                assert prog["type"] == "speedtest_progress"
                assert prog["phase"] == "up"
                assert prog["mbps"] == 9.8

                # 目标 agent 上报结果 → 浏览器 + 历史 + 会话结束
                a2.send_json({
                    "type": "speedtest_result", "room_id": room,
                    "data": {"type": "speedtest_result", "room_id": room,
                             "up_mbps": 9.7, "down_mbps": 42.1,
                             "up_bytes": 12125901, "down_bytes": 52625000,
                             "duration": 10},
                })
                result = browser_ctx.receive_json()
                assert result["type"] == "speedtest_result"
                assert result["up_mbps"] == 9.7
                assert result["down_mbps"] == 42.1
                assert room not in _agent_connections
                assert api_isolated._speedtest_active is None

    def test_st06_history_cap(self):
        """TC-ST06: 历史恒 ≤20 条, 且仅本人"""
        uid = register_user(_USER, _PASS, f"{_USER}@test.com").get("id", "")
        _speedtest_history.clear()
        for i in range(25):
            _speedtest_history.append({
                "source": "st_src", "target": "st_dst", "user_id": uid,
                "up_mbps": i, "down_mbps": i, "duration": 10, "finished_at": time.time(),
            })
        _speedtest_history.append({
            "source": "x", "target": "y", "user_id": "someone_else",
            "up_mbps": 0, "down_mbps": 0, "duration": 10, "finished_at": time.time(),
        })
        assert len(_speedtest_history) == 20  # deque maxlen
        resp = client.get("/api/webrtc/speedtest/history", headers=_headers())
        assert resp.status_code == 200
        hist = resp.json()["history"]
        assert len(hist) <= 20
        assert all(h["user_id"] == uid for h in hist)
        _speedtest_history.clear()

    def test_limits_allowed(self):
        """限速档白名单 10/50/100/0"""
        assert SPEEDTEST_LIMITS == (0, 10, 50, 100)
