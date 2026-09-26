"""L4 集成测试: 通过后端 SFTP Client API 连接真实 SSH/SFTP 服务器

验证后端 /api/sftp-client/* 端点与真实 SFTP 服务器的端到端链路。
"""
import os
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

SSH_HOST = os.environ.get("SSH_HOST", "127.0.0.1")
SSH_PORT = int(os.environ.get("SSH_PORT", "22"))
SSH_USER = os.environ.get("SSH_USER", "testuser")
SSH_PASS = os.environ.get("SSH_PASS", "testpass123")

client = TestClient(app)

_TS = int(time.time())
_USER = f"integ_sftp_{_TS}"
_PASS = "IntegSftp123!"


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


def _conn_params():
    return {
        "host": SSH_HOST,
        "port": SSH_PORT,
        "username": SSH_USER,
        "auth_type": "password",
        "password": SSH_PASS,
    }


def setup_module():
    _cleanup_user(_USER)
    register_user(_USER, _PASS, f"integ_sftp_{_TS}@test.com")


def teardown_module():
    _cleanup_user(_USER)


class TestRealSFTPClientList:
    """通过后端API列出真实SFTP目录"""

    def test_list_root(self):
        """L-07: 列出真实SFTP根目录"""
        resp = client.post("/api/sftp-client/list", json={
            **_conn_params(), "path": "/",
        }, headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "items" in data
        assert len(data["items"]) > 0

    def test_list_tmp(self):
        """L-08: 列出真实SFTP /tmp 目录"""
        resp = client.post("/api/sftp-client/list", json={
            **_conn_params(), "path": "/tmp",
        }, headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "items" in data


class TestRealSFTPClientRead:
    """通过后端API读取真实SFTP文件"""

    def test_read_write_cycle(self):
        """L-09: 真实SFTP文件写入+读取循环"""
        test_path = f"/tmp/e2e_api_test_{_TS}.txt"
        test_content = f"api integration test {_TS}"

        # 写入
        resp = client.post("/api/sftp-client/write", json={
            **_conn_params(), "path": test_path, "content": test_content,
        }, headers=_headers())
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        # 读取
        resp = client.post("/api/sftp-client/read", json={
            **_conn_params(), "path": test_path,
        }, headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "content" in data
        assert data["encoding"] == "base64"

        # 清理
        client.post("/api/sftp-client/delete", json={
            **_conn_params(), "path": test_path,
        }, headers=_headers())


class TestRealSFTPClientMkdir:
    """通过后端API创建真实SFTP目录"""

    def test_mkdir_and_delete(self):
        """L-10: 真实SFTP创建+删除目录"""
        test_dir = f"/tmp/e2e_api_dir_{_TS}"

        # 创建
        resp = client.post("/api/sftp-client/mkdir", json={
            **_conn_params(), "path": test_dir,
        }, headers=_headers())
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        # 验证存在 (通过list)
        resp = client.post("/api/sftp-client/list", json={
            **_conn_params(), "path": "/tmp",
        }, headers=_headers())
        assert resp.status_code == 200
        dir_names = [item["name"] for item in resp.json()["items"]]
        assert f"e2e_api_dir_{_TS}" in dir_names

        # 删除
        resp = client.post("/api/sftp-client/delete", json={
            **_conn_params(), "path": test_dir,
        }, headers=_headers())
        assert resp.status_code == 200


class TestRealSFTPClientUpload:
    """通过后端API上传文件到真实SFTP"""

    def test_upload_file(self):
        """L-11: 真实SFTP上传文件"""
        test_path = f"/tmp/e2e_upload_{_TS}.txt"

        resp = client.post("/api/sftp-client/upload",
            data={
                **_conn_params(), "path": test_path,
            },
            files={"file": ("upload_test.txt", b"upload content here", "text/plain")},
            headers=_headers(),
        )
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        # 验证文件存在
        resp = client.post("/api/sftp-client/stat", json={
            **_conn_params(), "path": test_path,
        }, headers=_headers())
        assert resp.status_code == 200

        # 清理
        client.post("/api/sftp-client/delete", json={
            **_conn_params(), "path": test_path,
        }, headers=_headers())


class TestRealSFTPClientRename:
    """通过后端API重命名真实SFTP文件"""

    def test_rename_file(self):
        """L-12: 真实SFTP重命名文件"""
        old_path = f"/tmp/e2e_rename_old_{_TS}.txt"
        new_name = f"e2e_rename_new_{_TS}.txt"

        # 创建源文件
        client.post("/api/sftp-client/write", json={
            **_conn_params(), "path": old_path, "content": "rename me",
        }, headers=_headers())

        # 重命名
        resp = client.post("/api/sftp-client/rename", json={
            **_conn_params(), "old_path": old_path, "new_name": new_name,
        }, headers=_headers())
        assert resp.status_code == 200
        assert resp.json()["ok"] is True

        # 清理
        new_path = f"/tmp/{new_name}"
        client.post("/api/sftp-client/delete", json={
            **_conn_params(), "path": new_path,
        }, headers=_headers())
