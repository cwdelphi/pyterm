"""
一次性迁移脚本：将 ./md/ 目录下的文档迁移到 admin 用户的 MinIO + DB
运行方式: docker exec pyterm_md python -m app.migrate_docs
"""
import asyncio
import json
import os
import uuid
from datetime import datetime, timezone
from pathlib import Path

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_mod
from app.database import init_db, Document, LinkGroup, LinkItem, User
from app.minio_client import ensure_bucket, upload_doc


MD_ROOT = Path(os.environ.get("MD_ROOT", str(Path(__file__).resolve().parent.parent / "md"))).resolve()


async def migrate_docs(db: AsyncSession, admin_id: str):
    now = datetime.now(timezone.utc).isoformat()
    count = {"dirs": 0, "files": 0}

    def scan_tree(root: Path, parent_id: str | None, parent_path: str = ""):
        entries = sorted(root.iterdir(), key=lambda p: (p.is_file(), p.name.casefold()))
        for p in entries:
            if p.name.startswith(".") or p.name == "data":
                continue
            rel = f"{parent_path}/{p.name}" if parent_path else p.name
            doc_id = f"doc_{uuid.uuid4().hex[:12]}"
            if p.is_dir():
                db.add(Document(
                    id=doc_id, user_id=admin_id, name=p.name, path=rel,
                    is_dir=True, parent_id=parent_id, created_at=now,
                ))
                count["dirs"] += 1
                scan_tree(p, doc_id, rel)
            elif p.suffix.lower() in {".md", ".txt", ".markdown"}:
                content = p.read_bytes()
                minio_key = upload_doc(admin_id, doc_id, p.name, content)
                db.add(Document(
                    id=doc_id, user_id=admin_id, name=p.name, path=rel,
                    is_dir=False, parent_id=parent_id, size=len(content),
                    minio_key=minio_key, created_at=now, updated_at=now,
                ))
                count["files"] += 1

    if MD_ROOT.is_dir():
        scan_tree(MD_ROOT, None)
    await db.commit()
    print(f"文档迁移完成: {count['dirs']} 个目录, {count['files']} 个文件")


async def migrate_links(db: AsyncSession, admin_id: str):
    now = datetime.now(timezone.utc).isoformat()
    base = MD_ROOT / "users"
    count = {"groups": 0, "links": 0}

    if base.is_dir():
        for user_dir in base.iterdir():
            if not user_dir.is_dir():
                continue
            links_file = user_dir / "links.json"
            if not links_file.exists():
                links_file = user_dir / "links.json.bak"
            if not links_file.exists():
                continue
            try:
                data = json.loads(links_file.read_text())
                groups = data.get("groups", []) if isinstance(data, dict) else []
                for g in groups:
                    gid = f"lg_{uuid.uuid4().hex[:8]}"
                    db.add(LinkGroup(id=gid, user_id=admin_id, name=g.get("name", "未分组"), created_at=now))
                    await db.flush()
                    count["groups"] += 1
                    for item in g.get("links", []):
                        db.add(LinkItem(
                            id=item.get("id", f"li_{uuid.uuid4().hex[:8]}"),
                            user_id=admin_id, group_id=gid,
                            title=item.get("title", ""),
                            url=item.get("url", ""),
                            description=item.get("description", ""),
                            created_at=now,
                        ))
                        count["links"] += 1
            except Exception as e:
                print(f"迁移 {links_file} 失败: {e}")

    shared = MD_ROOT / "_shared"
    links_file = shared / "links.json"
    if links_file.exists():
        try:
            data = json.loads(links_file.read_text())
            groups = data.get("groups", []) if isinstance(data, dict) else []
            for g in groups:
                gid = f"lg_{uuid.uuid4().hex[:8]}"
                db.add(LinkGroup(id=gid, user_id=admin_id, name=g.get("name", "未分组"), created_at=now))
                await db.flush()
                count["groups"] += 1
                for item in g.get("links", []):
                    db.add(LinkItem(
                        id=item.get("id", f"li_{uuid.uuid4().hex[:8]}"),
                        user_id=admin_id, group_id=gid,
                        title=item.get("title", ""),
                        url=item.get("url", ""),
                        description=item.get("description", ""),
                        created_at=now,
                    ))
                    count["links"] += 1
        except Exception as e:
            print(f"迁移 {links_file} 失败: {e}")

    await db.commit()
    print(f"链接迁移完成: {count['groups']} 个分组, {count['links']} 个链接")


async def main():
    print("=== 开始迁移 ===")
    print(f"MD_ROOT = {MD_ROOT}")

    await init_db()
    ensure_bucket()

    async with db_mod._session_factory() as db:
        result = await db.execute(select(User).where(User.role == "admin").limit(1))
        admin = result.scalar_one_or_none()
        if not admin:
            print("错误: 未找到 admin 用户")
            return
        print(f"Admin 用户: {admin.username} ({admin.id})")

        await migrate_docs(db, admin.id)
        await migrate_links(db, admin.id)

    print("=== 迁移完成 ===")
    print("请验证后手动清理 ./md/ 目录")


if __name__ == "__main__":
    asyncio.run(main())
