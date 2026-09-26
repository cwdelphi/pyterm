"""文档管理 API 测试 — 对应现网 /api/docs/*（原 /api/files/* 死路由已迁）。

MinIO 内容读写在宿主不可达时，对 upload/download/delete 打桩，仅测路由与 DB 语义。
"""
from unittest.mock import patch

import pytest
from fastapi.testclient import TestClient

from app.main import app
from app.tests.sync_db import register_user, delete_user_by_username

client = TestClient(app)

_IMPORT_TS = __import__("time").time()
_USER = f"docs_api_{int(_IMPORT_TS)}"
_PASS = "DocsApi123!"


def setup_module():
    delete_user_by_username(_USER)
    register_user(_USER, _PASS, f"{_USER}@test.com")


def teardown_module():
    delete_user_by_username(_USER)


def _headers() -> dict:
    resp = client.post("/api/auth/login", json={"username": _USER, "password": _PASS})
    assert resp.status_code == 200, resp.text
    return {"Authorization": f"Bearer {resp.json()['token']}"}


def _cleanup_path(h: dict, path: str):
    client.post("/api/docs/delete", json={"path": path}, headers=h)


class TestDocsAuth:
    def test_tree_requires_auth(self):
        resp = client.get("/api/docs/tree")
        assert resp.status_code in (401, 403)

    def test_write_requires_auth(self):
        resp = client.post("/api/docs/write", json={"path": "_x.md", "content": "x"})
        assert resp.status_code in (401, 403)


class TestDocsTree:
    def test_returns_list(self):
        resp = client.get("/api/docs/tree", headers=_headers())
        assert resp.status_code == 200
        assert isinstance(resp.json(), list)


class TestDocsReadWrite:
    def test_write_and_read_back(self):
        h = _headers()
        path = f"_test_docs_{int(_IMPORT_TS)}.md"
        try:
            with patch("app.api_isolated.upload_doc", return_value=f"stub/{path}"), \
                 patch("app.api_isolated.download_doc", return_value=b"# Test"), \
                 patch("app.api_isolated.delete_doc"):
                resp = client.post("/api/docs/write", json={"path": path, "content": "# Test"}, headers=h)
                assert resp.status_code == 200, resp.text
                assert resp.json()["ok"] is True

                resp = client.get("/api/docs/content", params={"path": path}, headers=h)
                assert resp.status_code == 200, resp.text
                assert resp.json()["content"] == "# Test"
        finally:
            with patch("app.api_isolated.delete_doc"):
                _cleanup_path(h, path)

    def test_read_nonexistent(self):
        resp = client.get("/api/docs/content", params={"path": "nonexistent.md"}, headers=_headers())
        assert resp.status_code == 404


class TestDocsNew:
    def test_create_file(self):
        h = _headers()
        path = f"_test_docs_new_{int(_IMPORT_TS)}.md"
        try:
            with patch("app.api_isolated.upload_doc", return_value=f"stub/{path}"):
                resp = client.post(
                    "/api/docs/write",
                    json={"path": path, "content": ""},
                    headers=h,
                )
                assert resp.status_code == 200, resp.text
                assert resp.json()["ok"] is True
        finally:
            with patch("app.api_isolated.delete_doc"):
                _cleanup_path(h, path)

    def test_create_dir(self):
        h = _headers()
        path = f"_test_docs_dir_{int(_IMPORT_TS)}"
        try:
            resp = client.post("/api/docs/mkdir", json={"path": path}, headers=h)
            assert resp.status_code == 200, resp.text
            assert resp.json()["ok"] is True
        finally:
            with patch("app.api_isolated.delete_doc"):
                _cleanup_path(h, path)


class TestDocsRename:
    def test_rename_file(self):
        h = _headers()
        src = f"_rename_src_{int(_IMPORT_TS)}.md"
        dst = f"_rename_dst_{int(_IMPORT_TS)}.md"
        try:
            with patch("app.api_isolated.upload_doc", return_value=f"stub/{src}"):
                client.post("/api/docs/write", json={"path": src, "content": "r"}, headers=h)
            resp = client.post(
                "/api/docs/rename",
                json={"old_path": src, "new_name": dst},
                headers=h,
            )
            assert resp.status_code == 200, resp.text
            assert resp.json()["path"] == dst
        finally:
            with patch("app.api_isolated.delete_doc"):
                _cleanup_path(h, dst)


class TestDocsDelete:
    def test_delete_file(self):
        h = _headers()
        path = f"_delete_me_{int(_IMPORT_TS)}.md"
        with patch("app.api_isolated.upload_doc", return_value=f"stub/{path}"):
            client.post("/api/docs/write", json={"path": path, "content": "d"}, headers=h)
        with patch("app.api_isolated.delete_doc"):
            resp = client.post("/api/docs/delete", json={"path": path}, headers=h)
        assert resp.status_code == 200, resp.text
        assert resp.json()["ok"] is True
