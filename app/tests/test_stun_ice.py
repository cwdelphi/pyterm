"""L1(延迟) STUN 可达性过滤测试

覆盖:
- _stun_entries: 本机 STUN 前置、不可达的不下发、全不可达时保底保留本机项
- _stun_reachable: 未探测 fail-open / 命中缓存按结果 / 缓存过期 fail-open
- _stun_probe: 非法主机不抛异常返回 False
"""
import time

from app.auth import (
    _stun_entries, _stun_reachable, _stun_probe,
    _stun_cache, _stun_lock, STUN_PROBE_TTL,
)


def _urls(entries: list) -> list:
    out = []
    for e in entries:
        u = e.get("urls", [])
        out.extend(u if isinstance(u, list) else [u])
    return out


class TestStunEntries:
    def test_unreachable_google_dropped(self):
        """Google STUN 不可达 → 不下发, 本机项保留且在最前"""
        def reachable(host, port):
            return "google" not in host

        entries = _stun_entries("1.2.3.4", 19302, reachable)
        urls = _urls(entries)
        assert any("1.2.3.4:19302" in u for u in urls)
        assert not any("google" in u for u in urls)
        # 本机前置
        assert "1.2.3.4" in urls[0]

    def test_all_unreachable_keeps_local(self):
        """全部不可达也不能清空 STUN 列表(保底保留本机项)"""
        entries = _stun_entries("1.2.3.4", 19302, lambda h, p: False)
        assert len(entries) == 1
        assert "1.2.3.4:19302" in entries[0]["urls"][0]

    def test_local_unreachable_google_ok(self):
        """本机不可达但 Google 可达 → 只留 Google"""
        entries = _stun_entries("1.2.3.4", 19302, lambda h, p: "google" in h)
        urls = _urls(entries)
        assert any("google" in u for u in urls)
        assert not any("1.2.3.4" in u for u in urls)

    def test_all_reachable_keeps_both(self):
        """全部可达 → 本机在前, Google 在后"""
        entries = _stun_entries("1.2.3.4", 19302, lambda h, p: True)
        urls = _urls(entries)
        assert "1.2.3.4" in urls[0]
        assert any("google" in u for u in urls)


class TestStunReachable:
    def test_fail_open_then_cached(self):
        key_host, key_port = "9.9.9.9", 19302
        with _stun_lock:
            _stun_cache.pop(f"{key_host}:{key_port}", None)
        # 未探测 → fail-open(等后台线程补)
        assert _stun_reachable(key_host, key_port) is True
        # 命中缓存 → 按结果
        with _stun_lock:
            _stun_cache[f"{key_host}:{key_port}"] = (False, time.time())
        assert _stun_reachable(key_host, key_port) is False
        # 过期 → fail-open
        with _stun_lock:
            _stun_cache[f"{key_host}:{key_port}"] = (False, time.time() - STUN_PROBE_TTL - 1)
        assert _stun_reachable(key_host, key_port) is True
        with _stun_lock:
            _stun_cache.pop(f"{key_host}:{key_port}", None)


class TestStunProbe:
    def test_probe_bad_host_no_raise(self):
        assert _stun_probe("no-such-host.invalid", 19302) is False
        assert _stun_probe("", 19302) is False
