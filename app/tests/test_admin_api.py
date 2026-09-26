"""管理员API测试 - 5个用例"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import _read_users_file, _write_users_file, register_user


def _cleanup_user(username: str):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_ADMIN_USER = f"admin_test_{int(_IMPORT_TS)}"
_NORMAL_USER = f"normal_test_{int(_IMPORT_TS)}"
_ADMIN_PASS = "Admin123!"
_NORMAL_PASS = "Normal123!"


def setup_module():
    _cleanup_user(_ADMIN_USER)
    _cleanup_user(_NORMAL_USER)
    register_user(_ADMIN_USER, _ADMIN_PASS, "admintest@test.com")
    register_user(_NORMAL_USER, _NORMAL_PASS, "normaltest@test.com")
    # 设为管理员
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == _ADMIN_USER:
            u["role"] = "admin"
    _write_users_file(data)


def teardown_module():
    _cleanup_user(_ADMIN_USER)
    _cleanup_user(_NORMAL_USER)


def _login(username, password):
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    return resp.json()["token"]


def _admin_headers():
    return {"Authorization": f"Bearer {_login(_ADMIN_USER, _ADMIN_PASS)}"}


def _normal_headers():
    return {"Authorization": f"Bearer {_login(_NORMAL_USER, _NORMAL_PASS)}"}


def _get_user_id(username):
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == username:
            return u["id"]
    return None


class TestAdminListUsers:
    def test_admin_can_list_users(self):
        """管理员获取用户列表"""
        resp = client.get("/api/admin/users", headers=_admin_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "users" in data
        assert isinstance(data["users"], list)
        assert len(data["users"]) >= 2

    def test_normal_user_cannot_list(self):
        """非管理员拒绝访问"""
        resp = client.get("/api/admin/users", headers=_normal_headers())
        assert resp.status_code == 403


class TestAdminToggleUser:
    def test_admin_toggle_user_status(self):
        """管理员禁用/启用用户"""
        uid = _get_user_id(_NORMAL_USER)
        assert uid is not None

        resp = client.put(f"/api/admin/users/{uid}/status", headers=_admin_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "is_active" in data

        # 恢复
        client.put(f"/api/admin/users/{uid}/status", headers=_admin_headers())


class TestAdminDeleteUser:
    def test_admin_cannot_delete_self(self):
        """管理员不能删除自己"""
        uid = _get_user_id(_ADMIN_USER)
        resp = client.delete(f"/api/admin/users/{uid}", headers=_admin_headers())
        assert resp.status_code == 400
        assert "自己" in resp.json()["detail"]

    def test_admin_delete_other_user(self):
        """管理员删除其他用户"""
        # 创建临时用户
        temp_user = f"temp_del_{int(_IMPORT_TS)}"
        _cleanup_user(temp_user)
        register_user(temp_user, "Temp123!", "temp@test.com")
        uid = _get_user_id(temp_user)

        resp = client.delete(f"/api/admin/users/{uid}", headers=_admin_headers())
        assert resp.status_code == 200

        # 验证已删除
        data = _read_users_file()
        assert not any(u["username"] == temp_user for u in data["users"])

        _cleanup_user(temp_user)
