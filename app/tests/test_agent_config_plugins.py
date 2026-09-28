"""Agent配置 plugins 端到端协议测试 — TC-CFG01~04 (阶段B′ 去SSH留协议)"""
import json
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import _read_users_file, _write_users_file, register_user

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_ADMIN_USER = f"admin_cfgplugins_{int(_IMPORT_TS)}"
_ADMIN_PASS = "Admin123!"
_AGENT_ID = f"agt_cfgplugins_{int(_IMPORT_TS)}"

_TCP_TUNNEL = {
    "id": "tn1", "name": "tcp-1", "protocol": "tcp", "local_port": 13389,
    "target_addr": "10.0.0.1:3389", "target_agent_id": "agt_b", "enabled": True,
}
_SOCKS_TUNNEL = {
    "id": "sn1", "name": "socks-1", "protocol": "socks5", "local_port": 12080,
    "target_addr": "", "target_agent_id": "agt_b", "enabled": True,
    "socks_username": "u", "socks_password": "p",
}


def _cleanup_user(username: str):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_ADMIN_USER)
    register_user(_ADMIN_USER, _ADMIN_PASS, "cfgplugins@test.com")
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == _ADMIN_USER:
            u["role"] = "admin"
    _write_users_file(data)


def teardown_module():
    _cleanup_user(_ADMIN_USER)


def _headers():
    resp = client.post("/api/auth/login", json={"username": _ADMIN_USER, "password": _ADMIN_PASS})
    return {"Authorization": f"Bearer {resp.json()['token']}"}


def _seed_config(config: dict):
    """直写 agent.config_json(模拟存量库数据)"""
    from sqlalchemy import update
    from app.database import Agent
    from app.tests.sync_db import _make_sync_factories
    engine, Session = _make_sync_factories()
    with Session() as s:
        s.execute(update(Agent).where(Agent.id == _AGENT_ID)
                  .values(config_json=json.dumps(config, ensure_ascii=False)))
        s.commit()
    engine.dispose()


def _read_raw_config() -> dict:
    from sqlalchemy import select
    from app.database import Agent
    from app.tests.sync_db import _make_sync_factories
    engine, Session = _make_sync_factories()
    with Session() as s:
        row = s.execute(select(Agent).where(Agent.id == _AGENT_ID)).scalar_one()
        raw = row.config_json
    engine.dispose()
    return json.loads(raw) if raw else {}


class TestAgentConfigPlugins:
    def setup_method(self):
        resp = client.post("/api/admin/agents",
                           json={"id": _AGENT_ID, "name": "cfg-plugins-test"}, headers=_headers())
        assert resp.status_code in (200, 400), resp.text  # 400=已存在(重复setup容忍)

    def teardown_method(self):
        client.delete(f"/api/admin/agents/{_AGENT_ID}", headers=_headers())

    def test_cfg01_get_lazy_migration_no_ssh_key(self):
        """TC-CFG01: 旧 config_json(顶层tunnels) GET → plugins 视图, 无 ssh 键"""
        _seed_config({"log_level": "info", "tunnels": [_TCP_TUNNEL, _SOCKS_TUNNEL]})
        resp = client.get(f"/api/admin/agents/{_AGENT_ID}/config", headers=_headers())
        assert resp.status_code == 200
        plugins = resp.json()["config"]["plugins"]
        assert set(plugins.keys()) == {"tunnel", "socks5"}
        assert "ssh" not in plugins
        assert plugins["tunnel"]["tunnels"] == [_TCP_TUNNEL]
        assert plugins["socks5"]["tunnels"] == [_SOCKS_TUNNEL]

    def test_cfg02_put_legacy_ssh_key_stripped(self):
        """TC-CFG02: PUT 含存量 plugins.ssh 键 → 接受并剥离, 落库无 ssh"""
        _seed_config({"log_level": "info", "tunnels": []})
        payload = {
            "log_level": "info",
            "plugins": {
                "tunnel": {"tunnels": [_TCP_TUNNEL]},
                "ssh": {"enabled": True, "port": 8822, "auth_mode": "both", "password": "x"},
            },
        }
        resp = client.put(f"/api/admin/agents/{_AGENT_ID}/config",
                          json=payload, headers=_headers())
        assert resp.status_code == 200, resp.text
        raw = _read_raw_config()
        assert set(raw["plugins"].keys()) == {"tunnel"}
        assert "ssh" not in raw["plugins"]
        # GET 视图同样无 ssh
        got = client.get(f"/api/admin/agents/{_AGENT_ID}/config", headers=_headers())
        assert "ssh" not in got.json()["config"]["plugins"]

    def test_cfg03_put_unknown_plugin_key_400(self):
        """TC-CFG03: PUT 未知插件键 → 400"""
        resp = client.put(f"/api/admin/agents/{_AGENT_ID}/config",
                          json={"plugins": {"unknown_plugin": {}}}, headers=_headers())
        assert resp.status_code == 400
        assert "unknown" in resp.text

    def test_cfg04_put_legacy_tunnels_split_and_roundtrip(self):
        """TC-CFG04: 旧客户端仅传 tunnels → 按 protocol 归桶; GET→PUT 回写一致"""
        _seed_config({"log_level": "info", "tunnels": []})
        resp = client.put(f"/api/admin/agents/{_AGENT_ID}/config",
                          json={"log_level": "info",
                                "tunnels": [_TCP_TUNNEL, _SOCKS_TUNNEL]},
                          headers=_headers())
        assert resp.status_code == 200, resp.text
        raw = _read_raw_config()
        assert raw["plugins"]["tunnel"]["tunnels"] == [_TCP_TUNNEL]
        assert raw["plugins"]["socks5"]["tunnels"] == [_SOCKS_TUNNEL]
        # GET→PUT 回写幂等
        got = client.get(f"/api/admin/agents/{_AGENT_ID}/config", headers=_headers())
        plugins = got.json()["config"]["plugins"]
        resp2 = client.put(f"/api/admin/agents/{_AGENT_ID}/config",
                           json={"log_level": "info", "plugins": plugins}, headers=_headers())
        assert resp2.status_code == 200, resp2.text
        assert _read_raw_config()["plugins"] == plugins

    def test_mirror_dual_format_push(self):
        """双格式下发: plugins 无顶层 tunnels → 附加合并 tunnels 镜像(旧Agent兼容)"""
        from app.api_isolated import _agent_config_with_mirror
        cfg = {"log_level": "info",
               "plugins": {"tunnel": {"tunnels": [_TCP_TUNNEL]},
                           "socks5": {"tunnels": [_SOCKS_TUNNEL]}}}
        out = _agent_config_with_mirror(cfg)
        assert [t["protocol"] for t in out["tunnels"]] == ["tcp", "socks5"]
        # 已有顶层 tunnels 或无 plugins → 原样
        assert _agent_config_with_mirror({"tunnels": []}) == {"tunnels": []}
