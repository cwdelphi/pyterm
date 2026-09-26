"""链接管理API测试 (需要认证)"""
import pytest
from fastapi.testclient import TestClient
from app.main import app
from app.tests.sync_db import register_user, _read_users_file, _write_users_file

client = TestClient(app)

_TS = __import__("time").time()
_USER = f"links_test_{int(_TS)}"
_PASS = "Links123!"


def _cleanup_user(username):
    data = _read_users_file()
    data["users"] = [u for u in data["users"] if u["username"] != username]
    _write_users_file(data)


def setup_module():
    _cleanup_user(_USER)
    register_user(_USER, _PASS, "links@test.com")


def teardown_module():
    _cleanup_user(_USER)


def _headers():
    resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
    return {"Authorization": f"Bearer {resp.json()['token']}"}


class TestLinksList:
    def test_returns_groups(self):
        resp = client.get("/api/links", headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert "groups" in data
        assert isinstance(data["groups"], list)


class TestLinksGroupAdd:
    def test_add_group(self):
        resp = client.post("/api/links/group/add", json={"name": "test_group"}, headers=_headers())
        assert resp.status_code == 200
        data = resp.json()
        assert data["ok"] is True
        group_id = data["id"]
        client.post("/api/links/group/delete", json={"id": group_id}, headers=_headers())


class TestLinksGroupRename:
    def test_rename_group(self):
        resp = client.post("/api/links/group/add", json={"name": "rename_me"}, headers=_headers())
        group_id = resp.json()["id"]
        resp = client.post("/api/links/group/rename", json={"id": group_id, "name": "renamed"}, headers=_headers())
        assert resp.status_code == 200
        client.post("/api/links/group/delete", json={"id": group_id}, headers=_headers())


class TestLinksAdd:
    def test_add_link(self):
        resp = client.post("/api/links/group/add", json={"name": "link_test_group"}, headers=_headers())
        gid = resp.json()["id"]
        resp = client.post("/api/links/add", json={
            "title": "Test Link", "url": "https://example.com",
            "description": "test", "group_id": gid,
        }, headers=_headers())
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
        client.post("/api/links/group/delete", json={"id": gid}, headers=_headers())


class TestLinksDelete:
    def test_delete_link(self):
        resp = client.post("/api/links/group/add", json={"name": "del_test_group"}, headers=_headers())
        gid = resp.json()["id"]
        resp = client.post("/api/links/add", json={
            "title": "Delete Me", "url": "https://example.com", "group_id": gid,
        }, headers=_headers())
        link_id = resp.json()["item"]["id"]
        resp = client.post("/api/links/delete", json={"id": link_id}, headers=_headers())
        assert resp.status_code == 200
        client.post("/api/links/group/delete", json={"id": gid}, headers=_headers())
