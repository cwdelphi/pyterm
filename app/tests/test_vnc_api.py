"""VNC连接管理 - 单元测试 (异步模式)

使用 httpx.AsyncClient 避免 TestClient 的事件循环冲突。
测试 connection_type=vnc 的连接在 CRUD 操作中的行为。
"""
import asyncio
import time
import pytest
from httpx import AsyncClient, ASGITransport
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file


_TS = int(time.time())
_USER = f"vnc_test_{_TS}"
_PASS = "VncTest123!"


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_USER)
    register_user(_USER, _PASS, f"vnc_{_TS}@test.com")


def teardown_module():
    _cleanup_user(_USER)


async def _get_token(client):
    resp = await client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
    return resp.json()["token"]


def _run(coro):
    return asyncio.get_event_loop().run_until_complete(coro)


# ═══════════════════════════════════════════════════════════
#  V-01 ~ V-03: 添加 VNC 连接
# ═══════════════════════════════════════════════════════════


class TestVncAdd:
    def test_v01_add_vnc_connection(self):
        """V-01: 添加 connection_type=vnc 连接"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_server", "host": "10.0.0.1", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_port": 5900,
                    "vnc_password": "vncPass123", "pixel_format": "tight",
                    "color_depth": "full", "read_only": False,
                }, headers=h)
                assert resp.status_code == 200
                data = resp.json()
                assert data["ok"] is True
                conn_id = data["id"]

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["connection_type"] == "vnc"
                assert found["vnc_port"] == 5900
                assert found["has_password"] is True
                assert found["pixel_format"] == "tight"
                assert found["color_depth"] == "full"
                assert found["read_only"] is False

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())

    def test_v02_vnc_defaults(self):
        """V-02: VNC 连接默认值正确"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_defaults", "host": "10.0.0.2", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["connection_type"] == "vnc"
                assert found["vnc_port"] == 5900
                assert found["pixel_format"] == "tight"
                assert found["color_depth"] == "full"
                assert found["read_only"] is False

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())

    def test_v03_ssh_type_default(self):
        """V-03: 不传 connection_type 默认 ssh"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "ssh_default", "host": "10.0.0.3", "port": 22,
                    "username": "root", "auth_type": "password", "password": "x",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["connection_type"] == "ssh"

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())


# ═══════════════════════════════════════════════════════════
#  V-04 ~ V-05: 更新 VNC 字段
# ═══════════════════════════════════════════════════════════


class TestVncUpdate:
    def test_v04_update_vnc_fields(self):
        """V-04: 更新 VNC 像素格式和色彩深度"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_update", "host": "10.0.0.4", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_port": 5900,
                    "pixel_format": "tight", "color_depth": "full",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.post("/api/ssh/update", json={
                    "id": conn_id, "name": "vnc_updated", "host": "10.0.0.4",
                    "port": 22, "username": "", "auth_type": "password",
                    "connection_type": "vnc", "vnc_port": 5901,
                    "pixel_format": "ZRLE", "color_depth": "low", "read_only": True,
                }, headers=h)
                assert resp.status_code == 200

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["name"] == "vnc_updated"
                assert found["vnc_port"] == 5901
                assert found["pixel_format"] == "ZRLE"
                assert found["color_depth"] == "low"
                assert found["read_only"] is True

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())

    def test_v05_update_preserves_vnc_password(self):
        """V-05: 更新时 vnc_password 空保留原密码"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_pw", "host": "10.0.0.5", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_password": "keep_secret",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.post("/api/ssh/update", json={
                    "id": conn_id, "name": "vnc_pw_updated", "host": "10.0.0.5",
                    "port": 22, "username": "", "auth_type": "password",
                    "connection_type": "vnc", "vnc_password": "",
                }, headers=h)
                assert resp.status_code == 200

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["has_password"] is True
                assert found["name"] == "vnc_pw_updated"

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())


# ═══════════════════════════════════════════════════════════
#  V-06: 类型切换 (SSH -> VNC)
# ═══════════════════════════════════════════════════════════


class TestVncTypeSwitch:
    def test_v06_switch_ssh_to_vnc(self):
        """V-06: 将 SSH 连接切换为 VNC 类型"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "switch_test", "host": "10.0.0.6", "port": 22,
                    "username": "root", "auth_type": "password", "password": "old_pw",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.post("/api/ssh/update", json={
                    "id": conn_id, "name": "switched_to_vnc", "host": "10.0.0.6",
                    "port": 22, "username": "", "auth_type": "password",
                    "connection_type": "vnc", "vnc_port": 5900,
                    "vnc_password": "new_vnc_pw",
                }, headers=h)
                assert resp.status_code == 200

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert found["connection_type"] == "vnc"
                assert found["vnc_port"] == 5900

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())


# ═══════════════════════════════════════════════════════════
#  V-07: 用户隔离
# ═══════════════════════════════════════════════════════════


class TestVncIsolation:
    def test_v07_user_isolation(self):
        """V-07: 用户 A 的 VNC 连接不出现在用户 B 列表"""
        user_b = f"vnc_b_{_TS}"
        pass_b = "VncB123!"
        register_user(user_b, pass_b, f"vnc_b_{_TS}@test.com")
        try:
            async def run():
                transport = ASGITransport(app=app)
                async with AsyncClient(transport=transport, base_url="http://test") as c:
                    token_a = await _get_token(c)
                    h_a = {"Authorization": f"Bearer {token_a}"}
                    resp_a = await c.post("/api/ssh/add", json={
                        "name": "a_vnc", "host": "10.0.0.7", "port": 22,
                        "username": "", "auth_type": "password", "password": "",
                        "connection_type": "vnc", "vnc_port": 5900,
                        "vnc_password": "secret_a",
                    }, headers=h_a)
                    conn_a_id = resp_a.json()["id"]

                    resp_b = await c.post("/api/auth/login", json={"username": user_b, "password": pass_b})
                    h_b = {"Authorization": f"Bearer {resp_b.json()['token']}"}
                    resp_b = await c.get("/api/ssh", headers=h_b)
                    ids_b = [x["id"] for x in resp_b.json()]
                    assert conn_a_id not in ids_b

                    await c.post("/api/ssh/delete", json={"id": conn_a_id}, headers=h_a)
            _run(run())
        finally:
            data = _read_users_file()
            data["users"] = [u for u in data["users"] if u["username"] != user_b]
            _write_users_file(data)


# ═══════════════════════════════════════════════════════════
#  V-08: 列表不暴露密码
# ═══════════════════════════════════════════════════════════


class TestVncListSecurity:
    def test_v08_no_password_in_list(self):
        """V-08: 列表中 VNC 密码不以明文暴露"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_sec", "host": "10.0.0.8", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_password": "super_secret",
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert "vnc_password" not in found or found.get("vnc_password", "") == ""
                assert found["has_password"] is True

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())


# ═══════════════════════════════════════════════════════════
#  V-09: 删除 VNC 连接
# ═══════════════════════════════════════════════════════════


class TestVncDelete:
    def test_v09_delete_vnc_connection(self):
        """V-09: 删除 VNC 连接"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_del", "host": "10.0.0.9", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_port": 5900,
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
                assert resp.status_code == 200

                resp = await c.get("/api/ssh", headers=h)
                found = any(x["id"] == conn_id for x in resp.json())
                assert not found
        _run(run())


# ═══════════════════════════════════════════════════════════
#  V-10: VNC 端口字段在列表中
# ═══════════════════════════════════════════════════════════


class TestVncPortField:
    def test_v10_vnc_port_in_list(self):
        """V-10: VNC 连接列表包含 vnc_port 字段"""
        async def run():
            transport = ASGITransport(app=app)
            async with AsyncClient(transport=transport, base_url="http://test") as c:
                token = await _get_token(c)
                h = {"Authorization": f"Bearer {token}"}
                resp = await c.post("/api/ssh/add", json={
                    "name": "vnc_port", "host": "10.0.0.10", "port": 22,
                    "username": "", "auth_type": "password", "password": "",
                    "connection_type": "vnc", "vnc_port": 5901,
                }, headers=h)
                conn_id = resp.json()["id"]

                resp = await c.get("/api/ssh", headers=h)
                found = next(x for x in resp.json() if x["id"] == conn_id)
                assert "vnc_port" in found
                assert found["vnc_port"] == 5901

                await c.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
        _run(run())
