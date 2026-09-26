"""SSH管理API测试 (需要认证)"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

client = TestClient(app)

_TS = __import__("time").time()
_USER = f"ssh_test_{int(_TS)}"
_PASS = "Ssh123!"


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_USER)
    register_user(_USER, _PASS, "ssh@test.com")


def teardown_module():
    _cleanup_user(_USER)


def _headers():
    resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
    return {"Authorization": f"Bearer {resp.json()['token']}"}


class TestSSHList:
    def test_returns_list(self):
        resp = client.get("/api/ssh", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert isinstance(data, list)

    def test_no_password_in_list(self):
        resp = client.get("/api/ssh", headers=_headers())
        data = resp.json()
        for conn in data:
            assert "password" not in conn


class TestSSHAdd:
    def test_add_connection(self):
        resp = client.post("/api/ssh/add", json={
            "name": "test_conn", "host": "127.0.0.1", "port": 22,
            "username": "test", "auth_type": "password", "password": "test123",
        }, headers=_headers())
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        conn_id = resp.json()["id"]
        resp = client.get("/api/ssh", headers=_headers())
        found = any(c["id"] == conn_id for c in resp.json())
        assert found
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=_headers())


class TestSSHUpdate:
    def test_update_preserves_password(self):
        resp = client.post("/api/ssh/add", json={
            "name": "update_test", "host": "127.0.0.1", "port": 22,
            "username": "test", "auth_type": "password", "password": "keep_me",
        }, headers=_headers())
        conn_id = resp.json()["id"]
        resp = client.post("/api/ssh/update", json={
            "id": conn_id, "name": "updated_name", "host": "127.0.0.1", "port": 22,
            "username": "test", "auth_type": "password", "password": "",
        }, headers=_headers())
        assert resp.status_code == 200
        resp = client.get("/api/ssh", headers=_headers())
        conn = next(c for c in resp.json() if c["id"] == conn_id)
        assert conn["has_password"] is True
        assert conn["name"] == "updated_name"
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=_headers())


class TestSSHDelete:
    def test_delete_connection(self):
        resp = client.post("/api/ssh/add", json={
            "name": "delete_me", "host": "127.0.0.1", "port": 22,
            "username": "test", "auth_type": "password", "password": "x",
        }, headers=_headers())
        conn_id = resp.json()["id"]
        resp = client.post("/api/ssh/delete", json={"id": conn_id}, headers=_headers())
        assert resp.status_code == 200
        resp = client.get("/api/ssh", headers=_headers())
        found = any(c["id"] == conn_id for c in resp.json())
        assert not found
