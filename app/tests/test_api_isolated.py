"""隔离API测试 - 6个用例"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_USER_A = f"iso_a_{int(_IMPORT_TS)}"
_USER_B = f"iso_b_{int(_IMPORT_TS)}"
_PASS_A = "IsoA123!"
_PASS_B = "IsoB123!"


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_USER_A)
    _cleanup_user(_USER_B)
    register_user(_USER_A, _PASS_A, "iso_a@test.com")
    register_user(_USER_B, _PASS_B, "iso_b@test.com")


def teardown_module():
    _cleanup_user(_USER_A)
    _cleanup_user(_USER_B)


def _login(username, password):
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    return resp.json()["token"]


def _headers_a():
    return {"Authorization": f"Bearer {_login(_USER_A, _PASS_A)}"}


def _headers_b():
    return {"Authorization": f"Bearer {_login(_USER_B, _PASS_B)}"}


class TestAuthRequired:
    def test_ssh_list_requires_auth(self):
        """未认证访问被拒绝"""
        resp = client.get("/api/ssh")
        assert resp.status_code in (401, 403)

    def test_ssh_list_with_auth(self):
        """认证后可访问"""
        resp = client.get("/api/ssh", headers=_headers_a())
        assert resp.status_code == 200
        assert isinstance(resp.json(), list)


class TestDataIsolation:
    def test_user_data_isolation(self):
        """用户数据隔离"""
        resp = client.post("/api/ssh/add", json={
            "name": "iso_test_a", "host": "10.0.0.1", "port": 22,
            "username": "root", "auth_type": "password", "password": "pass_a",
        }, headers=_headers_a())
        conn_id_a = resp.json()["id"]

        resp_b = client.get("/api/ssh", headers=_headers_b())
        conn_names_b = [c["name"] for c in resp_b.json()]
        assert "iso_test_a" not in conn_names_b

        resp_a = client.get("/api/ssh", headers=_headers_a())
        conn_names_a = [c["name"] for c in resp_a.json()]
        assert "iso_test_a" in conn_names_a

        client.post("/api/ssh/delete", json={"id": conn_id_a}, headers=_headers_a())


class TestPublicEndpoints:
    def test_log_endpoint_no_auth(self):
        """日志API无需认证"""
        resp = client.post("/api/log", json={"level": "info", "message": "test isolation"})
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
