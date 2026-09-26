"""L4 集成测试: asyncssh 直连真实 SSH 服务器

这些测试验证后端 API 与真实 SSH 服务器的端到端链路。
在 Docker Compose 测试环境中运行，SSH_HOST 指向 sshd 容器。
"""
import os
import time
import pytest
import asyncssh
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

SSH_HOST = os.environ.get("SSH_HOST", "127.0.0.1")
SSH_PORT = int(os.environ.get("SSH_PORT", "22"))
SSH_USER = os.environ.get("SSH_USER", "testuser")
SSH_PASS = os.environ.get("SSH_PASS", "testpass123")

client = TestClient(app)

_TS = int(time.time())
_USER = f"integ_ssh_{_TS}"
_PASS = "IntegSsh123!"


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


def setup_module():
    _cleanup_user(_USER)
    register_user(_USER, _PASS, f"integ_ssh_{_TS}@test.com")


def teardown_module():
    _cleanup_user(_USER)


# ═══════════════════════════════════════════════════════════
#  直连 asyncssh 测试 (不经过后端API)
# ═══════════════════════════════════════════════════════════


class TestRealSSHDirect:
    """直接用 asyncssh 连接真实 SSH 服务器"""

    @pytest.mark.asyncio
    async def test_connect_and_run_command(self):
        """L-01: 真实SSH连接 + 执行命令"""
        async with asyncssh.connect(
            SSH_HOST, SSH_PORT,
            username=SSH_USER, password=SSH_PASS,
            known_hosts=None
        ) as conn:
            result = await conn.run("echo hello_real_ssh")
            assert result.exit_status == 0
            assert "hello_real_ssh" in result.stdout

    @pytest.mark.asyncio
    async def test_sftp_scandir(self):
        """L-02: 真实SFTP列出目录"""
        async with asyncssh.connect(
            SSH_HOST, SSH_PORT,
            username=SSH_USER, password=SSH_PASS,
            known_hosts=None
        ) as conn:
            async with conn.start_sftp_client() as sftp:
                items = []
                async for entry in sftp.scandir("/"):
                    items.append(entry.filename)
                assert len(items) > 0
                assert "." in items or "tmp" in items

    @pytest.mark.asyncio
    async def test_sftp_file_operations(self):
        """L-03: 真实SFTP文件读写"""
        test_path = f"/tmp/e2e_integ_test_{_TS}.txt"
        test_content = f"integration test content {_TS}"
        async with asyncssh.connect(
            SSH_HOST, SSH_PORT,
            username=SSH_USER, password=SSH_PASS,
            known_hosts=None
        ) as conn:
            async with conn.start_sftp_client() as sftp:
                # 写入
                async with sftp.open(test_path, "w") as f:
                    await f.write(test_content)

                # 读取
                async with sftp.open(test_path, "r") as f:
                    content = await f.read()
                assert content == test_content

                # stat
                st = await sftp.stat(test_path)
                assert st.size > 0

                # 删除
                await sftp.remove(test_path)

    @pytest.mark.asyncio
    async def test_sftp_mkdir_rmdir(self):
        """L-04: 真实SFTP创建/删除目录"""
        test_dir = f"/tmp/e2e_integ_dir_{_TS}"
        async with asyncssh.connect(
            SSH_HOST, SSH_PORT,
            username=SSH_USER, password=SSH_PASS,
            known_hosts=None
        ) as conn:
            async with conn.start_sftp_client() as sftp:
                await sftp.mkdir(test_dir)
                st = await sftp.stat(test_dir)
                assert st is not None
                await sftp.rmdir(test_dir)


# ═══════════════════════════════════════════════════════════
#  通过后端 API 测试 (端到端)
# ═══════════════════════════════════════════════════════════


@pytest.fixture(scope="module")
def ctx():
    """with TestClient(app) 上下文, 触发 lifespan 使 WS 可用"""
    with TestClient(app) as c:
        yield c



class TestRealSSHTestEndpoint:
    def test_ssh_test_connection_real(self):
        """L-06: 测试连接API指向真实服务器"""
        resp = client.post("/api/ssh/test", json={
            "host": SSH_HOST, "port": SSH_PORT,
            "username": SSH_USER, "auth_type": "password",
            "password": SSH_PASS,
        }, headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert data["ok"] is True
