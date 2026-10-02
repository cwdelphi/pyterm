"""连接诊断 DC 链路标注 (P2P/relay/BUG) 测试 — TC-WP01~06"""
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import _read_users_file, _write_users_file, register_user, delete_user_by_username

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_USER = f"test_webrtcpath_{int(_IMPORT_TS)}"
_PASS = "Normal123!"
_OTHER = f"test_webrtcpath2_{int(_IMPORT_TS)}"
_OTHER_PASS = "Normal123!"
_ROOM = f"room_wp_{int(_IMPORT_TS)}"


def _cleanup_user(username: str):
    delete_user_by_username(username)
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_USER)
    _cleanup_user(_OTHER)
    register_user(_USER, _PASS, "webrtcpath@test.com")
    register_user(_OTHER, _OTHER_PASS, "webrtcpath2@test.com")


def teardown_module():
    _cleanup_user(_USER)
    _cleanup_user(_OTHER)


def _login(username: str, password: str) -> dict:
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    return {"Authorization": f"Bearer {resp.json()['token']}"}


def _headers() -> dict:
    return _login(_USER, _PASS)


def _other_headers() -> dict:
    return _login(_OTHER, _OTHER_PASS)


def _report(room_id: str, **kw):
    body = {
        "room_id": room_id, "conn_name": "wp-test", "conn_type": "ssh",
        "host": "10.0.0.9", "port": 22, "username": "root",
        "agent_tcp_ms": 12, "agent_ssh_ms": 8, "agent_shell_ms": 5,
        "duration_total": 100,
    }
    body.update(kw)
    return client.post("/api/timeline/report", json=body, headers=_headers())


def _detail(room_id: str) -> dict:
    resp = client.post("/api/timeline/detail", json={"room_id": room_id}, headers=_headers())
    assert resp.status_code == 200, resp.text
    return resp.json()["connection"]


class TestWebRtcPath:
    def test_wp01_late_patch_only_touches_column(self):
        """TC-WP01: 首报无值 → 补报只写 webrtc_path，列表/详情可见"""
        room = f"{_ROOM}_1"
        assert _report(room).status_code == 200
        assert _detail(room)["webrtc_path"] == ""
        steps_before = client.post("/api/timeline/detail", json={"room_id": room},
                                   headers=_headers()).json()["steps"]
        resp = client.post("/api/timeline/webrtc-path",
                           json={"room_id": room, "webrtc_path": "P2P"}, headers=_headers())
        assert resp.status_code == 200, resp.text
        assert resp.json()["webrtc_path"] == "P2P"
        detail = client.post("/api/timeline/detail", json={"room_id": room},
                             headers=_headers()).json()
        assert detail["connection"]["webrtc_path"] == "P2P"
        assert detail["steps"] == steps_before, "补报不应重算 steps"
        lst = client.post("/api/timeline/records", json={"webrtc_path": "P2P"},
                          headers=_headers()).json()
        assert any(i["room_id"] == room and i["webrtc_path"] == "P2P" for i in lst["items"])
        lst_none = client.post("/api/timeline/records", json={"webrtc_path": "none"},
                               headers=_headers()).json()
        assert all(i["room_id"] != room for i in lst_none["items"])

    def test_wp02_initial_report_carries_value(self):
        """TC-WP02: 检测先于首报时，值随 /report 直接落库"""
        room = f"{_ROOM}_2"
        assert _report(room, webrtc_path="relay").status_code == 200
        assert _detail(room)["webrtc_path"] == "relay"

    def test_wp03_invalid_value_400(self):
        """TC-WP03: 非法标注值 → 400"""
        room = f"{_ROOM}_3"
        assert _report(room).status_code == 200
        resp = client.post("/api/timeline/webrtc-path",
                           json={"room_id": room, "webrtc_path": "DIRECT"}, headers=_headers())
        assert resp.status_code == 400

    def test_wp04_other_users_room_404(self):
        """TC-WP04: 不能补报他人记录"""
        room = f"{_ROOM}_4"
        assert _report(room).status_code == 200
        resp = client.post("/api/timeline/webrtc-path",
                           json={"room_id": room, "webrtc_path": "P2P"}, headers=_other_headers())
        assert resp.status_code == 404

    def test_wp05_empty_not_overwrite(self):
        """TC-WP05: 空值补报不覆盖已有标注"""
        room = f"{_ROOM}_5"
        assert _report(room, webrtc_path="BUG").status_code == 200
        resp = client.post("/api/timeline/webrtc-path",
                           json={"room_id": room, "webrtc_path": "BUG"}, headers=_headers())
        assert resp.status_code == 200
        assert _detail(room)["webrtc_path"] == "BUG"

    def test_wp06_stats_include_p2p_rate(self):
        """TC-WP06: 统计含 p2p_count/relay_count/p2p_rate"""
        room = f"{_ROOM}_6"
        assert _report(room, webrtc_path="P2P").status_code == 200
        resp = client.post("/api/timeline/stats", json={}, headers=_headers())
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["p2p_count"] >= 1
        assert "relay_count" in data
        assert isinstance(data["p2p_rate"], (int, float))
        assert 0 <= data["p2p_rate"] <= 100

    def test_wp07_stats_percentiles_and_stages(self):
        """L3: 统计含 p50/p95 长尾分位数与分阶段聚合"""
        room = f"{_ROOM}_7"
        assert _report(room, duration_total=120, webrtc_path="P2P").status_code == 200
        resp = client.post("/api/timeline/stats", json={}, headers=_headers())
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert "avg_total" in data
        assert isinstance(data["p50_total"], (int, float))
        assert isinstance(data["p95_total"], (int, float))
        assert data["p95_total"] >= data["p50_total"]
        assert isinstance(data["stages"], list)
        for st in data["stages"]:
            assert {"from_node", "to_node", "protocol", "action",
                    "count", "avg", "p50", "p95"} <= set(st)
            assert st["count"] > 0
            assert st["p95"] >= st["p50"]
