"""
共享测试fixtures
- admin_user: 创建管理员用户并获取token
- normal_user: 创建普通用户并获取token
- client: FastAPI TestClient
"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.auth import _hash_password, _generate_token
from app.tests.sync_db import _read_users_file, _write_users_file, register_user

client = TestClient(app)


def _cleanup_user(username: str):
    """清理测试用户"""
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


@pytest.fixture(scope="module")
def admin_user():
    """创建管理员用户并获取token"""
    username = "test_admin_baseline"
    password = "Admin123!"
    email = "admin@test.com"

    # 清理旧数据
    _cleanup_user(username)

    # 注册
    result = register_user(username, password, email)

    # 设为管理员
    data = _read_users_file()
    for u in data["users"]:
        if u["username"] == username:
            u["role"] = "admin"
            break
    _write_users_file(data)

    # 登录获取token
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    token = resp.json()["token"]

    yield {"token": token, "user": result, "username": username, "password": password}

    # 清理
    _cleanup_user(username)


@pytest.fixture(scope="module")
def normal_user():
    """创建普通用户并获取token"""
    username = "test_normal_baseline"
    password = "Normal123!"
    email = "normal@test.com"

    _cleanup_user(username)

    result = register_user(username, password, email)

    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    token = resp.json()["token"]

    yield {"token": token, "user": result, "username": username, "password": password}

    _cleanup_user(username)


@pytest.fixture(scope="module")
def second_user():
    """创建第二个普通用户（用于隔离测试）"""
    username = "test_second_baseline"
    password = "Second123!"
    email = "second@test.com"

    _cleanup_user(username)

    result = register_user(username, password, email)

    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    token = resp.json()["token"]

    yield {"token": token, "user": result, "username": username, "password": password}

    _cleanup_user(username)
