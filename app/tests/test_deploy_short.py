"""模式二短链部署 /a/d/<code>、/a/s/<code> 测试 — TC-DS01~08"""
import re
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import _read_users_file, _write_users_file, register_user, delete_user_by_username

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_ADMIN_USER = f"test_shortlink_admin_{int(_IMPORT_TS)}"
_ADMIN_PASS = "Admin123!"
_NORMAL_USER = f"test_shortlink_user_{int(_IMPORT_TS)}"
_NORMAL_PASS = "Normal123!"
_AGENT_ID = f"agt_shortlink_{int(_IMPORT_TS)}"

_CODE_RE = re.compile(r"^[A-Za-z0-9_-]{8}$")


def _cleanup_user(username: str):
    delete_user_by_username(username)
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_ADMIN_USER)
    _cleanup_user(_NORMAL_USER)
    register_user(_ADMIN_USER, _ADMIN_PASS, "shortlink_admin@test.com")
    register_user(_NORMAL_USER, _NORMAL_PASS, "shortlink_user@test.com")
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == _ADMIN_USER:
            u["role"] = "admin"
    _write_users_file(data)


def teardown_module():
    _cleanup_user(_ADMIN_USER)
    _cleanup_user(_NORMAL_USER)


def _login(username: str, password: str) -> dict:
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    return {"Authorization": f"Bearer {resp.json()['token']}"}


def _admin() -> dict:
    return _login(_ADMIN_USER, _ADMIN_PASS)


def _normal() -> dict:
    return _login(_NORMAL_USER, _NORMAL_PASS)


def _purge_links():
    from sqlalchemy import delete
    from app.database import DeployLink
    from app.tests.sync_db import _make_sync_factories
    engine, Session = _make_sync_factories()
    with Session() as s:
        s.execute(delete(DeployLink).where(DeployLink.agent_id == _AGENT_ID))
        s.commit()
    engine.dispose()


class TestDeployShortLink:
    def setup_method(self):
        resp = client.post("/api/admin/agents",
                           json={"id": _AGENT_ID, "name": "short-link-test"}, headers=_admin())
        assert resp.status_code in (200, 400), resp.text  # 400=已存在(容忍重跑)
        _purge_links()

    def teardown_method(self):
        client.delete(f"/api/admin/agents/{_AGENT_ID}", headers=_admin())
        _purge_links()

    def _short(self) -> dict:
        resp = client.post("/api/deploy/agent/short", json={"id": _AGENT_ID}, headers=_admin())
        assert resp.status_code == 200, resp.text
        return resp.json()

    def test_ds01_short_paths_shape(self):
        """TC-DS01: 签发返回 /a/d/<code> 与 /a/s/<code>，同一 code，8 位随机"""
        data = self._short()
        assert _CODE_RE.match(data["code"]), data["code"]
        assert data["path_docker"] == f"/a/d/{data['code']}"
        assert data["path_systemd"] == f"/a/s/{data['code']}"
        assert data["expires_in"] == 1800

    def test_ds02_docker_script(self):
        """TC-DS02: /a/d/<code> 返回 docker 部署脚本"""
        data = self._short()
        resp = client.get(data["path_docker"])
        assert resp.status_code == 200, resp.text
        assert "docker run" in resp.text
        assert f'AGENT_ID="{_AGENT_ID}"' in resp.text
        assert "wragent.service" not in resp.text

    def test_ds03_systemd_script(self):
        """TC-DS03: /a/s/<code> 返回 systemd 部署脚本（模式由路径区分）"""
        data = self._short()
        resp = client.get(data["path_systemd"])
        assert resp.status_code == 200, resp.text
        assert "wragent.service" in resp.text
        assert "systemctl" in resp.text
        assert "docker run" not in resp.text

    def test_ds04_repeatable_within_ttl(self):
        """TC-DS04: 30 分钟内可重复执行（无单次消费限制）"""
        data = self._short()
        for _ in range(2):
            assert client.get(data["path_docker"]).status_code == 200
            assert client.get(data["path_systemd"]).status_code == 200

    def test_ds05_invalid_mode_or_code_404(self):
        """TC-DS05: 非法模式字符 / 伪造 code → 404"""
        data = self._short()
        assert client.get(f"/a/x/{data['code']}").status_code == 404
        assert client.get("/a/d/zzzzzzzz").status_code == 404
        assert client.get(f"/a/d/{data['code']}/extra").status_code in (404, 405)

    def test_ds06_requires_login(self):
        """TC-DS06: 未登录签发 → 401（agent:manage 覆盖普通用户，与既有部署接口一致）"""
        resp = client.post("/api/deploy/agent/short", json={"id": _AGENT_ID})
        assert resp.status_code == 401
        resp = client.post("/api/deploy/agent/short", json={"id": _AGENT_ID}, headers=_normal())
        assert resp.status_code == 200, resp.text

    def test_ds07_persisted_in_db(self):
        """TC-DS07: code 落 deploy_links 表（非内存），md 重启后仍可解析"""
        data = self._short()
        from sqlalchemy import select
        from app.database import DeployLink
        from app.tests.sync_db import _make_sync_factories
        engine, Session = _make_sync_factories()
        with Session() as s:
            row = s.execute(select(DeployLink).where(DeployLink.code == data["code"])).scalar_one_or_none()
        engine.dispose()
        assert row is not None, "deploy_links 未落库"
        assert row.agent_id == _AGENT_ID

    def test_ds08_expired_404(self):
        """TC-DS08: 过期 code → 404"""
        data = self._short()
        from sqlalchemy import update
        from app.database import DeployLink
        from app.tests.sync_db import _make_sync_factories
        engine, Session = _make_sync_factories()
        with Session() as s:
            s.execute(update(DeployLink).where(DeployLink.code == data["code"]).values(exp=1))
            s.commit()
        engine.dispose()
        assert client.get(data["path_docker"]).status_code == 404
