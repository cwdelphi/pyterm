"""S6: 凭据静态加密 — SSH/VNC/RDP 密码等落库前加密，读取时解密。

密钥来源（优先级）:
1. 环境变量 CREDENTIAL_KEY (Fernet key, urlsafe base64 32B)
2. 由 JWT_SECRET 派生（零配置可用；生产应单独设 CREDENTIAL_KEY）

密文带前缀 enc:v1: 以便与历史明文共存（读到明文则原样返回，写路径统一加密）。
"""
from __future__ import annotations

import base64
import hashlib
import logging
import os
from functools import lru_cache

from cryptography.fernet import Fernet, InvalidToken

logger = logging.getLogger(__name__)

_PREFIX = "enc:v1:"


@lru_cache(maxsize=1)
def _fernet() -> Fernet:
    key = os.environ.get("CREDENTIAL_KEY", "").strip()
    if key:
        try:
            # validate format early
            Fernet(key.encode("ascii"))
            return Fernet(key.encode("ascii"))
        except Exception:
            logger.warning("CREDENTIAL_KEY invalid, deriving from JWT_SECRET")
    secret = os.environ.get("JWT_SECRET", "change_me_jwt")
    # 32B urlsafe base64 key derived from JWT_SECRET
    digest = hashlib.sha256(("pyterm.cred.v1:" + secret).encode("utf-8")).digest()
    return Fernet(base64.urlsafe_b64encode(digest))


def encrypt_secret(value: str | None) -> str:
    """加密敏感字段；空值与已加密值原样返回。"""
    if not value:
        return value or ""
    if value.startswith(_PREFIX):
        return value
    token = _fernet().encrypt(value.encode("utf-8")).decode("ascii")
    return _PREFIX + token


def decrypt_secret(value: str | None) -> str:
    """解密敏感字段；非密文（历史明文/空）原样返回。"""
    if not value:
        return value or ""
    if not value.startswith(_PREFIX):
        return value
    try:
        return _fernet().decrypt(value[len(_PREFIX):].encode("ascii")).decode("utf-8")
    except (InvalidToken, Exception) as e:
        logger.error("credential decrypt failed: %s", e)
        return ""


def is_encrypted(value: str | None) -> bool:
    return bool(value) and value.startswith(_PREFIX)
