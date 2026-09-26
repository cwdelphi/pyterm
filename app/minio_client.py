"""
MinIO 客户端封装 — 文档内容存储
"""
import os
import logging
from io import BytesIO

from minio import Minio
from minio.error import S3Error

_logger = logging.getLogger("app.minio")

_BUCKET = os.environ.get("MINIO_BUCKET", "pyterm-docs")
_ENDPOINT = os.environ.get("MINIO_ENDPOINT", "minio:9000")
_ACCESS_KEY = os.environ.get("MINIO_ACCESS_KEY", "minioadmin")
_SECRET_KEY = os.environ.get("MINIO_SECRET_KEY", "change_me_minio")

_client: Minio | None = None


def _get_client() -> Minio:
    global _client
    if _client is None:
        _client = Minio(_ENDPOINT, access_key=_ACCESS_KEY, secret_key=_SECRET_KEY, secure=False)
    return _client


def ensure_bucket():
    """确保 bucket 存在，不存在则创建"""
    c = _get_client()
    if not c.bucket_exists(_BUCKET):
        c.make_bucket(_BUCKET)
        _logger.info("MinIO bucket '%s' created", _BUCKET)


def upload_doc(user_id: str, doc_id: str, filename: str, content: bytes) -> str:
    """上传文件，返回 minio_key"""
    key = f"{user_id}/{doc_id}/{filename}"
    c = _get_client()
    c.put_object(_BUCKET, key, BytesIO(content), length=len(content), content_type="text/plain; charset=utf-8")
    _logger.debug("MinIO upload: %s (%d bytes)", key, len(content))
    return key


def download_doc(minio_key: str) -> bytes:
    """下载文件内容"""
    c = _get_client()
    resp = c.get_object(_BUCKET, minio_key)
    try:
        return resp.read()
    finally:
        resp.close()
        resp.release_conn()


def delete_doc(minio_key: str):
    """删除单个文件"""
    c = _get_client()
    c.remove_object(_BUCKET, minio_key)
    _logger.debug("MinIO delete: %s", minio_key)


def delete_user_prefix(user_id: str):
    """删除用户所有文件"""
    c = _get_client()
    objects = c.list_objects(_BUCKET, prefix=f"{user_id}/", recursive=True)
    for obj in objects:
        c.remove_object(_BUCKET, obj.object_name)
    _logger.info("MinIO deleted all objects for user %s", user_id)
