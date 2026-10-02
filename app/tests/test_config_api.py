"""配置和日志API测试"""
import pytest
from fastapi.testclient import TestClient
from app.main import app

client = TestClient(app)


class TestConfig:
    def test_get_config(self):
        resp = client.get("/api/config")
        assert resp.status_code == 200
        data = resp.json()
        assert "site_name" in data
        assert "home" in data


class TestLogs:
    def test_get_logs_requires_auth(self):
        """S4: 服务端日志只读端点需 system:admin, 匿名 401
        (admin 200 / 普通用户 403 矩阵见 test_remote_isolation.py::TestS4LogsAuth)"""
        resp = client.get("/api/logs")
        assert resp.status_code == 401

    def test_post_frontend_log(self):
        resp = client.post("/api/log", json={
            "level": "info", "message": "test log entry",
        })
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
