"""SFTP 客户端 API 测试 (/api/sftp-client/*)

asyncssh.connect() 在 api_isolated.py 中的两种用法:
1. `async with asyncssh.connect(**kw) as conn:` (list/cwd/stat/read/write/mkdir/rename/delete/rmdir/chmod)
2. `conn = await asyncssh.connect(**kw)` (download)

关键 mock 要点:
- sftp.scandir() → async iterator (not coroutine)
- sftp.open() → async context manager (not coroutine)
- conn.wait_closed() → awaitable
"""
import time
import pytest
from unittest.mock import patch, AsyncMock, MagicMock
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file
import asyncssh

client = TestClient(app)

_TS = int(time.time())
_USER = f"sftp_cl_{_TS}"
_PASS = "SftpCl123!"


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
    register_user(_USER, _PASS, f"sftp_cl_{_TS}@test.com")


def teardown_module():
    _cleanup_user(_USER)


# ── Mock helpers ──────────────────────────────────────────


class _AsyncIter:
    """async iterator for scandir()"""
    def __init__(self, items):
        self._items = items
    def __aiter__(self):
        return self
    async def __anext__(self):
        if not self._items:
            raise StopAsyncIteration
        return self._items.pop(0)


class _AsyncCtxMgr:
    """async context manager for sftp.open()"""
    def __init__(self, obj):
        self._obj = obj
    async def __aenter__(self):
        return self._obj
    async def __aexit__(self, *a):
        return False


class _SFTPConnCtxMgr:
    """async context manager for conn.start_sftp_client()"""
    def __init__(self, sftp_mock):
        self._sftp = sftp_mock
    async def __aenter__(self):
        return self._sftp
    async def __aexit__(self, *a):
        return False


class _SSHConnCtxMgr:
    """async context manager for asyncssh.connect()"""
    def __init__(self, sftp_mock):
        self._sftp = sftp_mock
    async def __aenter__(self):
        conn = MagicMock()
        conn.start_sftp_client.return_value = _SFTPConnCtxMgr(self._sftp)
        conn.wait_closed = AsyncMock()
        conn.close = MagicMock()
        return conn
    async def __aexit__(self, *a):
        return False


def _connect_ctx(sftp_mock):
    """Return a callable that replaces asyncssh.connect — for `async with asyncssh.connect(**kw)`"""
    return MagicMock(return_value=_SSHConnCtxMgr(sftp_mock))


class _AwaitableConn:
    """For download endpoint: `conn = await asyncssh.connect(**kw)`"""
    def __init__(self, sftp_mock):
        self._sftp = sftp_mock
    async def __call__(self, **kw):
        conn = MagicMock()
        conn.start_sftp_client.return_value = _SFTPConnCtxMgr(self._sftp)
        conn.wait_closed = AsyncMock()
        conn.close = MagicMock()
        return conn


def _connect_awaitable(sftp_mock):
    """Return a callable that returns an awaitable — for `conn = await asyncssh.connect()`"""
    return MagicMock(side_effect=_AwaitableConn(sftp_mock))


def _mock_stat(filename, size=100, mode=0o100644, mtime=1234567890):
    entry = MagicMock()
    entry.filename = filename
    attrs = MagicMock()
    attrs.size = size
    attrs.permissions = mode
    attrs.mtime = mtime
    entry.attrs = attrs
    return entry


def _mock_file_obj(data: bytes):
    """Async file-like with read()"""
    f = MagicMock()
    f.read = AsyncMock(return_value=data)
    f.write = AsyncMock()
    return _AsyncCtxMgr(f)


def _mock_file_write():
    """Async file-like for writing"""
    f = MagicMock()
    f.write = AsyncMock()
    return _AsyncCtxMgr(f)


# ═══════════════════════════════════════════════════════════
#  A-28 ~ A-37
# ═══════════════════════════════════════════════════════════


class TestSFTPClientList:
    def test_sftp_client_list(self):
        """A-28: 列出远程目录"""
        sftp = MagicMock()
        entry = _mock_stat("test.txt", size=200, mode=0o100644)
        sftp.scandir = MagicMock(return_value=_AsyncIter([entry]))

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/list", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/",
            }, headers=_headers())
            assert resp.status_code == 200
            data = resp.json()
            assert "items" in data
            assert len(data["items"]) == 1
            assert data["items"][0]["name"] == "test.txt"
            assert data["items"][0]["size"] == 200


class TestSFTPClientRead:
    def test_sftp_client_read(self):
        """A-29: 读取远程文件"""
        sftp = MagicMock()
        stat_result = MagicMock()
        stat_result.size = 10
        sftp.stat = AsyncMock(return_value=stat_result)
        sftp.open = MagicMock(return_value=_mock_file_obj("hello world"))

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/read", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/test.txt",
            }, headers=_headers())
            assert resp.status_code == 200
            data = resp.json()
            assert "content" in data
            assert data["encoding"] == "base64"


class TestSFTPClientWrite:
    def test_sftp_client_write(self):
        """A-30: 写入远程文件"""
        sftp = MagicMock()
        sftp.open = MagicMock(return_value=_mock_file_write())

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/write", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass",
                "path": "/test.txt", "content": "new content",
            }, headers=_headers())
            assert resp.status_code == 200
            assert resp.json()["ok"] is True


class TestSFTPClientMkdir:
    def test_sftp_client_mkdir(self):
        """A-31: 创建远程目录"""
        sftp = MagicMock()
        sftp.mkdir = AsyncMock()

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/mkdir", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/newdir",
            }, headers=_headers())
            assert resp.status_code == 200
            assert resp.json()["ok"] is True


class TestSFTPClientDelete:
    def test_sftp_client_delete_file(self):
        """A-32a: 删除远程文件"""
        sftp = MagicMock()
        stat_result = MagicMock()
        stat_result.permissions = 0o100644
        sftp.stat = AsyncMock(return_value=stat_result)
        sftp.remove = AsyncMock()

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/delete", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/old.txt",
            }, headers=_headers())
            assert resp.status_code == 200
            assert resp.json()["ok"] is True

    def test_sftp_client_delete_dir(self):
        """A-32b: 删除远程目录"""
        sftp = MagicMock()
        stat_result = MagicMock()
        stat_result.permissions = 0o040755
        sftp.stat = AsyncMock(return_value=stat_result)
        sftp.rmdir = AsyncMock()

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/delete", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/olddir",
            }, headers=_headers())
            assert resp.status_code == 200
            assert resp.json()["ok"] is True


class TestSFTPClientRename:
    def test_sftp_client_rename(self):
        """A-33: 重命名远程文件"""
        sftp = MagicMock()
        sftp.rename = AsyncMock()

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/rename", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass",
                "old_path": "/old.txt", "new_name": "new.txt",
            }, headers=_headers())
            assert resp.status_code == 200
            assert resp.json()["ok"] is True


class TestSFTPClientUpload:
    def test_sftp_client_upload(self):
        """A-34: 上传文件"""
        sftp = MagicMock()
        sftp.open = MagicMock(return_value=_mock_file_write())

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/upload",
                data={
                    "host": "127.0.0.1", "port": "22", "username": "test",
                    "auth_type": "password", "password": "pass",
                    "path": "/upload.txt",
                },
                files={"file": ("test.txt", b"file content", "text/plain")},
                headers=_headers(),
            )
            assert resp.status_code == 200
            assert resp.json()["ok"] is True


class TestSFTPClientDownload:
    def test_sftp_client_download(self):
        """A-35: 下载文件 (download端点用 await connect + 手动__aenter__)"""
        sftp = MagicMock()
        f = MagicMock()
        f.read = AsyncMock(side_effect=[b"file data", b""])
        sftp.open = MagicMock(return_value=_AsyncCtxMgr(f))

        with patch.object(asyncssh, "connect", _connect_awaitable(sftp)):
            resp = client.post("/api/sftp-client/download", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/file.txt",
            }, headers=_headers())
            assert resp.status_code == 200


class TestSFTPClientNoAuth:
    def test_sftp_client_no_auth(self):
        """A-36: sftp-client/list 无 auth header 返回 401 (S1-S3 已加鉴权)"""
        sftp = MagicMock()
        sftp.scandir = MagicMock(return_value=_AsyncIter([]))

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/list", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/",
            })
            assert resp.status_code == 401


class TestSFTPClientMaxSize:
    def test_sftp_client_max_size(self):
        """A-37: 超大文件限制(10MB)"""
        sftp = MagicMock()
        stat_result = MagicMock()
        stat_result.size = 20 * 1024 * 1024  # 20MB
        sftp.stat = AsyncMock(return_value=stat_result)

        with patch.object(asyncssh, "connect", _connect_ctx(sftp)):
            resp = client.post("/api/sftp-client/read", json={
                "host": "127.0.0.1", "port": 22, "username": "test",
                "auth_type": "password", "password": "pass", "path": "/big.bin",
            }, headers=_headers())
            assert resp.status_code == 413
