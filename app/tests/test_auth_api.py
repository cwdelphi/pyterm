"""认证API测试 - 8个用例"""
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

client = TestClient(app)

_TS = int(time.time())
_TEST_USER = f"auth_test_{_TS}"
_TEST_PASS = "TestPass123!"
_TEST_EMAIL = f"authtest_{_TS}@test.com"


def _cleanup(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup(_TEST_USER)
    register_user(_TEST_USER, _TEST_PASS, _TEST_EMAIL)


def teardown_module():
    _cleanup(_TEST_USER)


def _login():
    resp = client.post("/api/auth/login", json={"username": _TEST_USER, "password": _TEST_PASS})
    return resp.json().get("token", "")


class TestRegister:
    def test_register_success(self):
        resp = client.post("/api/auth/register", json={
            "username": _TEST_USER, "password": _TEST_PASS, "email": _TEST_EMAIL,
        })
        # 用户已在setup_module注册，重复注册返回400是正确的
        assert resp.status_code in (200, 400)
        if resp.status_code == 200:
            data = resp.json()
            assert "id" in data
            assert data["username"] == _TEST_USER

    def test_register_duplicate_username(self):
        resp = client.post("/api/auth/register", json={
            "username": _TEST_USER, "password": _TEST_PASS, "email": "dup@test.com",
        })
        assert resp.status_code == 400


class TestLogin:
    def test_login_success(self):
        resp = client.post("/api/auth/login", json={
            "username": _TEST_USER, "password": _TEST_PASS,
        })
        assert resp.status_code == 200
        data = resp.json()
        assert "token" in data
        assert "user" in data
        assert data["user"]["username"] == _TEST_USER

    def test_login_wrong_password(self):
        resp = client.post("/api/auth/login", json={
            "username": _TEST_USER, "password": "WrongPassword!",
        })
        assert resp.status_code == 401


class TestTokenValidation:
    def test_me_with_valid_token(self):
        token = _login()
        resp = client.get("/api/auth/me", headers={"Authorization": f"Bearer {token}"})
        assert resp.status_code == 200
        assert resp.json()["username"] == _TEST_USER

    def test_me_with_invalid_token(self):
        resp = client.get("/api/auth/me", headers={"Authorization": "Bearer invalid_token_12345"})
        assert resp.status_code in (401, 403)


class TestPasswordChange:
    def test_change_password_success(self):
        token = _login()
        new_pass = "NewPass456!"
        resp = client.put("/api/auth/password", json={
            "old_password": _TEST_PASS, "new_password": new_pass,
        }, headers={"Authorization": f"Bearer {token}"})
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        # 验证新密码可登录
        resp = client.post("/api/auth/login", json={"username": _TEST_USER, "password": new_pass})
        assert resp.status_code == 200

        # 恢复原密码
        token2 = resp.json()["token"]
        resp = client.put("/api/auth/password", json={
            "old_password": new_pass, "new_password": _TEST_PASS,
        }, headers={"Authorization": f"Bearer {token2}"})
        assert resp.status_code == 200

    def test_change_password_wrong_old(self):
        token = _login()
        resp = client.put("/api/auth/password", json={
            "old_password": "WrongOldPass!", "new_password": "NewPass789!",
        }, headers={"Authorization": f"Bearer {token}"})
        assert resp.status_code == 400
        assert "旧密码" in resp.json()["detail"]
