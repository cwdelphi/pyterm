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
    def test_get_logs(self):
        resp = client.get("/api/logs")
        assert resp.status_code == 200
        data = resp.json()
        # api_isolated 返回 {"lines": [...]} 格式
        assert "lines" in data or "backend" in data

    def test_post_frontend_log(self):
        resp = client.post("/api/log", json={
            "level": "info", "message": "test log entry",
        })
        assert resp.status_code == 200
        assert resp.json()["ok"] is True
