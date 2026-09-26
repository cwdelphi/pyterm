"""P3: 凭据静态加密往返 / 幂等 / 遗留明文兼容 / API 剥离。"""
from fastapi.testclient import TestClient

from app.crypto import encrypt_secret, decrypt_secret, is_encrypted
from app.main import app

client = TestClient(app)


class TestCryptoRoundTrip:
    def test_round_trip(self):
        ct = encrypt_secret("s3cret-pw")
        assert ct.startswith("enc:v1:")
        assert is_encrypted(ct)
        assert not is_encrypted("plain")
        assert decrypt_secret(ct) == "s3cret-pw"

    def test_plaintext_passthrough(self):
        assert decrypt_secret("legacy_plain") == "legacy_plain"
        assert not is_encrypted("legacy_plain")

    def test_encrypt_idempotent(self):
        ct = encrypt_secret("once")
        assert encrypt_secret(ct) == ct
        assert decrypt_secret(ct) == "once"

    def test_empty(self):
        assert encrypt_secret("") == ""
        assert encrypt_secret(None) == ""
        assert decrypt_secret("") == ""
        assert decrypt_secret(None) == ""
        assert not is_encrypted("")

    def test_unicode(self):
        ct = encrypt_secret("密码🔑")
        assert decrypt_secret(ct) == "密码🔑"


class TestSSHListStripsSecrets:
    """列表 API 不回传密码字段；写路径落库为密文。"""

    def test_list_no_secret_fields(self, normal_user):
        token = client.post(
            "/api/auth/login",
            json={"username": normal_user["username"], "password": normal_user["password"]},
        ).json()["token"]
        h = {"Authorization": f"Bearer {token}"}
        resp = client.post(
            "/api/ssh/add",
            json={
                "name": "p3_enc", "host": "10.9.9.9", "port": 22,
                "username": "root", "auth_type": "password",
                "password": "enc_me_please",
                "vnc_password": "vnc_enc", "rdp_password": "rdp_enc",
                "connection_type": "ssh",
            },
            headers=h,
        )
        assert resp.status_code == 200
        conn_id = resp.json().get("id")
        resp = client.get("/api/ssh", headers=h)
        items = resp.json()
        found = next((c for c in items if c.get("id") == conn_id), None)
        assert found is not None
        assert "password" not in found
        assert "vnc_password" not in found
        assert "rdp_password" not in found
        assert found.get("has_password") is True
        # 取密码端点回传明文
        pw = client.get(f"/api/ssh/{conn_id}/password", headers=h)
        assert pw.status_code == 200
        assert pw.json().get("password") == "enc_me_please"
        client.post("/api/ssh/delete", json={"id": conn_id}, headers=h)
