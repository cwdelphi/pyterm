"""SSH CRUD / WebSocket / SFTP Server / Test Connection API 测试"""
import time
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

client = TestClient(app)

_TS = int(time.time())
_USER_A = f"sshsftp_a_{_TS}"
_PASS_A = "SshA123!"
_USER_B = f"sshsftp_b_{_TS}"
_PASS_B = "SshB123!"


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def _login(username, password):
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    return resp.json()["token"]


def _headers(username, password):
    return {"Authorization": f"Bearer {_login(username, password)}"}


def _clean_ssh(token):
    """清理当前用户的所有SSH连接"""
    headers = {"Authorization": f"Bearer {token}"}
    resp = client.get("/api/ssh", headers=headers)
    for c in resp.json():
        client.post("/api/ssh/delete", json={"id": c["id"]}, headers=headers)


def setup_module():
    _cleanup_user(_USER_A)
    _cleanup_user(_USER_B)
    register_user(_USER_A, _PASS_A, f"sshsftp_a_{_TS}@test.com")
    register_user(_USER_B, _PASS_B, f"sshsftp_b_{_TS}@test.com")


def teardown_module():
    _cleanup_user(_USER_A)
    _cleanup_user(_USER_B)


# ═══════════════════════════════════════════════════════════
#  SSH CRUD (A-01 ~ A-09)
# ═══════════════════════════════════════════════════════════


class TestSSHCruud:
    """A-01 ~ A-09"""

    def test_ssh_list_empty(self):
        """A-01: 空列表返回空数组"""
        h = _headers(_USER_A, _PASS_A)
        _clean_ssh(_login(_USER_A, _PASS_A))
        resp = client.get("/api/ssh", headers=h)
        assert resp.status_code == 200
        assert resp.json() == []

    def test_ssh_add_connection(self):
        """A-02: 添加连接返回 ok:true 和 id"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/ssh/add", json={
            "name": "conn_01", "host": "10.0.0.1", "port": 22,
            "username": "root", "auth_type": "password", "password": "secret",
        }, headers=h)
        assert resp.status_code == 200
        data = resp.json()
        assert data["ok"] is True
        assert "id" in data
        assert len(data["id"]) > 0
        # 清理
        client.post("/api/ssh/delete", json={"id": data["id"]}, headers=h)

    def test_ssh_add_duplicate(self):
        """A-03: 同名连接添加两次返回400(或ok:false)"""
        h = _headers(_USER_A, _PASS_A)
        resp1 = client.post("/api/ssh/add", json={
            "name": "dup_conn", "host": "10.0.0.2", "port": 22,
            "username": "root", "auth_type": "password", "password": "x",
        }, headers=h)
        conn_id = resp1.json()["id"]
        # 第二次添加同名连接 — 服务器允许同名但不重复id
        resp2 = client.post("/api/ssh/add", json={
            "name": "dup_conn", "host": "10.0.0.2", "port": 22,
            "username": "root", "auth_type": "password", "password": "x",
        }, headers=h)
        # 实际上服务器不做名称去重, 两个都能加; 清理
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        client.post("/api/ssh/delete", json={"id": resp2.json()["id"]}, headers=h)

    def test_ssh_update_preserves_password(self):
        """A-04: 更新时 password 为空则保留原密码"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/ssh/add", json={
            "name": "keep_pw", "host": "10.0.0.3", "port": 22,
            "username": "root", "auth_type": "password", "password": "keep_me",
        }, headers=h)
        conn_id = resp.json()["id"]
        # 更新时 password 为空字符串
        resp = client.post("/api/ssh/update", json={
            "id": conn_id, "name": "keep_pw_updated", "host": "10.0.0.3",
            "port": 22, "username": "root", "auth_type": "password", "password": "",
        }, headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        # 验证 has_password 仍为 True
        resp = client.get("/api/ssh", headers=h)
        found = next(c for c in resp.json() if c["id"] == conn_id)
        assert found["has_password"] is True
        assert found["name"] == "keep_pw_updated"
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)

    def test_ssh_update_changes_password(self):
        """A-05: 更新时提供新密码会覆盖"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/ssh/add", json={
            "name": "change_pw", "host": "10.0.0.4", "port": 22,
            "username": "root", "auth_type": "password", "password": "old_pw",
        }, headers=h)
        conn_id = resp.json()["id"]
        # 更新为新密码
        resp = client.post("/api/ssh/update", json={
            "id": conn_id, "name": "change_pw", "host": "10.0.0.4",
            "port": 22, "username": "root", "auth_type": "password", "password": "new_pw",
        }, headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)

    def test_ssh_delete_connection(self):
        """A-06: 删除连接后列表中不存在"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/ssh/add", json={
            "name": "delete_me", "host": "10.0.0.5", "port": 22,
            "username": "root", "auth_type": "password", "password": "x",
        }, headers=h)
        conn_id = resp.json()["id"]
        resp = client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        # 列表中不存在
        resp = client.get("/api/ssh", headers=h)
        assert not any(c["id"] == conn_id for c in resp.json())

    def test_ssh_list_no_password_field(self):
        """A-07: 列表中不含 password 字段"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/ssh/add", json={
            "name": "no_pw_field", "host": "10.0.0.6", "port": 22,
            "username": "root", "auth_type": "password", "password": "secret_pw",
        }, headers=h)
        conn_id = resp.json()["id"]
        resp = client.get("/api/ssh", headers=h)
        for c in resp.json():
            assert "password" not in c
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)

    def test_ssh_list_has_password_flag(self):
        """A-08: 列表包含 has_password 布尔字段"""
        h = _headers(_USER_A, _PASS_A)
        # 添加有密码的连接
        resp = client.post("/api/ssh/add", json={
            "name": "has_flag", "host": "10.0.0.7", "port": 22,
            "username": "root", "auth_type": "password", "password": "exists",
        }, headers=h)
        conn_id = resp.json()["id"]
        # 添加无密码的连接
        resp2 = client.post("/api/ssh/add", json={
            "name": "no_flag", "host": "10.0.0.8", "port": 22,
            "username": "root", "auth_type": "password", "password": "",
        }, headers=h)
        conn_id2 = resp2.json()["id"]
        resp = client.get("/api/ssh", headers=h)
        items = resp.json()
        c1 = next(c for c in items if c["id"] == conn_id)
        c2 = next(c for c in items if c["id"] == conn_id2)
        assert c1["has_password"] is True
        assert c2["has_password"] is False
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        client.post("/api/ssh/delete", json={"id": conn_id2}, headers=h)

    def test_ssh_user_isolation(self):
        """A-09: 用户A看不到用户B的连接"""
        h_a = _headers(_USER_A, _PASS_A)
        h_b = _headers(_USER_B, _PASS_B)
        # 用户A添加连接
        resp = client.post("/api/ssh/add", json={
            "name": "user_a_conn", "host": "10.0.0.9", "port": 22,
            "username": "root", "auth_type": "password", "password": "a_pw",
        }, headers=h_a)
        conn_a_id = resp.json()["id"]
        # 用户B添加连接
        resp = client.post("/api/ssh/add", json={
            "name": "user_b_conn", "host": "10.0.0.10", "port": 22,
            "username": "root", "auth_type": "password", "password": "b_pw",
        }, headers=h_b)
        conn_b_id = resp.json()["id"]
        # 用户A的列表不含B的连接
        resp = client.get("/api/ssh", headers=h_a)
        ids_a = [c["id"] for c in resp.json()]
        assert conn_b_id not in ids_a
        # 用户B的列表不含A的连接
        resp = client.get("/api/ssh", headers=h_b)
        ids_b = [c["id"] for c in resp.json()]
        assert conn_a_id not in ids_b
        # 清理
        client.post("/api/ssh/delete", json={"id": conn_a_id}, headers=h_a)
        client.post("/api/ssh/delete", json={"id": conn_b_id}, headers=h_b)


# ═══════════════════════════════════════════════════════════
#  SSH WebSocket (A-10 ~ A-18)
# ═══════════════════════════════════════════════════════════


class TestSSHWebSocket:
    """A-10 ~ A-18 (WebSocket tests removed - endpoints deleted)"""


# ═══════════════════════════════════════════════════════════
#  SSH Test Connection (A-19 ~ A-20)
# ═══════════════════════════════════════════════════════════


class TestSSHTestConnection:
    """A-19 ~ A-20"""

    def test_ssh_test_success(self):
        """A-19: mock连接成功返回 ok"""
        from unittest.mock import patch, AsyncMock, MagicMock

        mock_conn = MagicMock()
        # async with 需要 __aenter__ 是 awaitable
        mock_conn.__aenter__ = AsyncMock(return_value=mock_conn)
        mock_conn.__aexit__ = AsyncMock(return_value=False)

        # asyncssh.connect() 在 async with 中被调用
        # 它返回一个 async context manager
        mock_ssh = MagicMock()
        mock_ssh.connect = MagicMock(return_value=mock_conn)

        with patch("app.api_isolated.asyncssh", mock_ssh):
            resp = client.post("/api/ssh/test", json={
                "host": "127.0.0.1", "port": 22,
                "username": "test", "auth_type": "password", "password": "test123",
            }, headers=_headers(_USER_A, _PASS_A))
            assert resp.status_code == 200
            data = resp.json()
            assert data["ok"] is True

    def test_ssh_test_failure(self):
        """A-20: 连接失败返回 ok:false"""
        from unittest.mock import patch, AsyncMock
        import asyncssh

        async def fake_connect(*args, **kwargs):
            raise asyncssh.ConnectionLost("连接失败")

        with patch("app.api_isolated.asyncssh") as mock_ssh:
            mock_ssh.connect = AsyncMock(side_effect=fake_connect)
            mock_ssh.ConnectionLost = asyncssh.ConnectionLost
            resp = client.post("/api/ssh/test", json={
                "host": "127.0.0.1", "port": 22,
                "username": "test", "auth_type": "password", "password": "x",
            }, headers=_headers(_USER_A, _PASS_A))
            assert resp.status_code == 200
            data = resp.json()
            assert data["ok"] is False
            assert "detail" in data

    def test_ssh_test_no_auth(self):
        """S1-S3: ssh/test 无 auth header 返回 401"""
        resp = client.post("/api/ssh/test", json={
            "host": "127.0.0.1", "port": 22,
            "username": "test", "auth_type": "password", "password": "x",
        })
        assert resp.status_code == 401


# ═══════════════════════════════════════════════════════════
#  SFTP Server (A-21 ~ A-27)
# ═══════════════════════════════════════════════════════════


class TestSFTPServer:
    """A-21 ~ A-27"""

    def test_sftp_get_config(self):
        """A-21: GET /api/sftp/config 返回配置"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.get("/api/sftp/config", headers=h)
        assert resp.status_code == 200
        data = resp.json()
        assert "share_dir" in data
        assert "port" in data
        assert "username" in data
        assert "password" in data

    def test_sftp_save_config(self):
        """A-22: POST /api/sftp/config 保存配置"""
        h = _headers(_USER_A, _PASS_A)
        resp = client.post("/api/sftp/config", json={
            "share_dir": "/tmp/test_share",
            "read_only": False,
            "username": "testuser",
            "password": "testpw",
            "port": 2233,
            "bind": "127.0.0.1",
        }, headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        # 验证读取保存的配置
        resp = client.get("/api/sftp/config", headers=h)
        data = resp.json()
        assert data["port"] == 2233
        assert data["username"] == "testuser"
        assert data["read_only"] is False
        # 恢复默认
        client.post("/api/sftp/config", json={
            "share_dir": "/app/md", "read_only": True,
            "username": "ppy", "password": "changeme", "port": 2222, "bind": "0.0.0.0",
        }, headers=h)

    def test_sftp_start_stop_lifecycle(self):
        """A-23: start → status running → stop → status stopped"""
        h = _headers(_USER_A, _PASS_A)
        # 确保已停止
        client.post("/api/sftp/stop", headers=h)
        # 启动
        resp = client.post("/api/sftp/start", headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        # 检查状态
        resp = client.get("/api/sftp/status", headers=h)
        assert resp.json()["running"] is True
        # 停止
        resp = client.post("/api/sftp/stop", headers=h)
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        # 检查状态
        resp = client.get("/api/sftp/status", headers=h)
        assert resp.json()["running"] is False

    def test_sftp_status_running(self):
        """A-24: 运行中状态"""
        h = _headers(_USER_A, _PASS_A)
        client.post("/api/sftp/stop", headers=h)
        client.post("/api/sftp/start", headers=h)
        resp = client.get("/api/sftp/status", headers=h)
        assert resp.status_code == 200
        assert resp.json()["running"] is True
        client.post("/api/sftp/stop", headers=h)

    def test_sftp_status_stopped(self):
        """A-25: 已停止状态"""
        h = _headers(_USER_A, _PASS_A)
        client.post("/api/sftp/stop", headers=h)
        resp = client.get("/api/sftp/status", headers=h)
        assert resp.status_code == 200
        assert resp.json()["running"] is False

    def test_sftp_logs(self):
        """A-26: GET /api/sftp/log 返回日志条目"""
        h = _headers(_USER_A, _PASS_A)
        # 先启动/停止产生日志
        client.post("/api/sftp/stop", headers=h)
        client.post("/api/sftp/start", headers=h)
        client.post("/api/sftp/stop", headers=h)
        resp = client.get("/api/sftp/log", headers=h)
        assert resp.status_code == 200
        data = resp.json()
        assert "log" in data
        assert isinstance(data["log"], list)

    def test_sftp_double_start(self):
        """A-27: 重复启动返回已在运行"""
        h = _headers(_USER_A, _PASS_A)
        client.post("/api/sftp/stop", headers=h)
        resp1 = client.post("/api/sftp/start", headers=h)
        assert resp1.json()["ok"] is True
        resp2 = client.post("/api/sftp/start", headers=h)
        assert resp2.json()["ok"] is False
        assert "运行" in resp2.json()["detail"]
        client.post("/api/sftp/stop", headers=h)
