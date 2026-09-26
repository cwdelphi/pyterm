import json
from datetime import datetime
from typing import Optional, List

from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import BaseModel
from sqlalchemy import select, func, desc, and_
from sqlalchemy.exc import IntegrityError, OperationalError, DataError
from sqlalchemy.ext.asyncio import AsyncSession

from .database import get_db, ConnectionTimeline, TimelineStep
from .auth_api import get_current_user
from .i18n import t

timeline_router = APIRouter(prefix="/api/timeline", tags=["timeline"])


# ── Request/Response Models ──

class TimelineReport(BaseModel):
    room_id: str
    conn_name: str = ""
    conn_type: str = "ssh"
    host: str = ""
    port: int = 0
    username: str = ""
    path_mode: str = "direct"
    client_ip: str = ""
    agent_1_ip: str = ""
    agent_1_name: str = ""
    agent_2_ip: str = ""
    agent_2_name: str = ""
    gateway_ip: str = ""
    agent_id: str = ""
    duration_total: float = 0
    success: int = 1
    error_stage: str = ""
    error_msg: str = ""
    failed_step: int = 0
    completed_steps: int = 0
    total_steps: int = 0
    browser: str = ""
    os_info: str = ""
    connected_at: str = ""
    # Agent fine-grained timing
    agent_tcp_ms: float = 0
    agent_ssh_ms: float = 0
    agent_shell_ms: float = 0
    agent_connect_ms: float = 0
    agent_ssh_host: str = ""
    agent_ssh_port: int = 0
    # 前端置位：agent 成功诊断已到达（亚毫秒截断/证据兜底）
    agent_ok: int = 0
    # Browser timing (raw)
    t_start: float = 0
    t_ws_open: float = 0
    t_signal_ok: float = 0
    t_rtc_connected: float = 0
    t_dc_open: float = 0
    t_first_data: float = 0
    t_error: float = 0
    duration_ws: float = 0
    duration_signal: float = 0
    duration_ice: float = 0
    duration_dc: float = 0
    duration_data: float = 0


class TimelineQuery(BaseModel):
    page: int = 1
    page_size: int = 20
    conn_type: Optional[str] = None
    success: Optional[int] = None
    search: Optional[str] = None
    start_date: Optional[str] = None
    end_date: Optional[str] = None
    agent_id: Optional[str] = None


class TimelineDetailQuery(BaseModel):
    room_id: Optional[str] = None
    record_id: Optional[int] = None


# ── Timeline Step Definitions ──

def _pos(v) -> float:
    try:
        v = float(v or 0)
    except (TypeError, ValueError):
        return 0.0
    return v if v > 0 else 0.0


def _agent_evidence(report: TimelineReport) -> bool:
    """Agent 成功诊断已到达：全链路时长、前端 agent_ok 标志（亚毫秒截断为 0 的兜底），
    或 target host 已回传（错误诊断必带 error_stage，先走失败分支，不会借此误判成功）。"""
    return (_pos(report.agent_connect_ms) > 0 or (report.agent_ok or 0) > 0
            or bool(report.agent_ssh_host))


def _loopback_evidence(report: TimelineReport) -> bool:
    """闭环证据：DC 首字节已回传 → 数据通路（含 agent→目标）确实走通。"""
    return _pos(report.t_first_data) > 0 or _pos(report.duration_data) > 0


def _phase_ok(report: TimelineReport, phase: str) -> bool:
    """阶段是否发生：duration>0 或 对应时间戳>0。
    网关模式 rtcConnected 与 dcOpen 同刻打点（duration_dc=0）、共享 WS wsOpen 与 start
    同刻（duration_ws=0），仅看 duration 会把已发生的阶段误判为 skipped。"""
    if phase == "ws":
        return _pos(report.duration_ws) > 0 or _pos(report.t_ws_open) > 0
    if phase == "signal":
        return _pos(report.duration_signal) > 0 or _pos(report.t_signal_ok) > 0
    if phase == "ice":
        return _pos(report.duration_ice) > 0 or _pos(report.t_rtc_connected) > 0
    if phase == "dc":
        return _pos(report.duration_dc) > 0 or _pos(report.t_dc_open) > 0
    if phase == "data":
        return _loopback_evidence(report)
    if phase == "agent_tcp":
        return _pos(report.agent_tcp_ms) > 0 or _agent_evidence(report) or _loopback_evidence(report)
    if phase == "agent_ssh":
        return _pos(report.agent_ssh_ms) > 0 or _agent_evidence(report) or _loopback_evidence(report)
    if phase == "agent_shell":
        return _pos(report.agent_shell_ms) > 0 or _agent_evidence(report) or _loopback_evidence(report)
    return False


def _status(report: TimelineReport, stages: tuple, occurred: bool) -> str:
    """failed 优先：错误阶段即使测得时长也判失败；其次按阶段是否发生判 ok/skipped。"""
    if report.error_stage and report.error_stage in stages:
        return "failed"
    return "ok" if occurred else "skipped"


def _err(report: TimelineReport, stages: tuple) -> str:
    return report.error_msg if (report.error_stage and report.error_stage in stages) else ""


def _target_ok(report: TimelineReport) -> bool:
    """agent→目标 已连通（不含闭环自身：闭环数据不能自证目标连接，VNC 尤其如此）。"""
    return _pos(report.agent_tcp_ms) > 0 or _agent_evidence(report)


def _loopback_status(report: TimelineReport, target_ok: bool = True) -> str:
    """闭环步骤：dc 阶段错误→failed；首字节回传→ok（目标未证实→partial）；仅 agent 证据→partial。"""
    if report.error_stage == "dc":
        return "failed"
    if _loopback_evidence(report):
        return "ok" if target_ok else "partial"
    if _agent_evidence(report):
        return "partial"
    return "skipped"


def _compute_direct_ssh_steps(report: TimelineReport) -> list:
    total = max(report.duration_total, 1)
    steps = []
    idx = 0

    # Step 1: Browser -> Backend (WebSocket)
    idx += 1
    ws_ms = _pos(report.duration_ws)
    steps.append({
        "step_index": idx, "key": "ws", "from_node": "browser", "to_node": "backend",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.connect_agent_request"),
        "src_ip": report.client_ip, "dst_ip": "",
        "latency_ms": ws_ms, "latency_pct": round(ws_ms / total * 100, 1),
        "status": _status(report, ("ws",), _phase_ok(report, "ws")),
        "error_msg": _err(report, ("ws",)),
    })

    # Step 2: Backend -> Agent (WebSocket) — full signal window, no invented split
    idx += 1
    sig1 = _pos(report.duration_signal)
    steps.append({
        "step_index": idx, "key": "signal_fwd", "from_node": "backend", "to_node": "agent",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.browser_connect_forward"),
        "src_ip": "", "dst_ip": report.agent_1_ip or report.agent_2_ip or "",
        "latency_ms": sig1, "latency_pct": round(sig1 / total * 100, 1),
        "status": _status(report, ("signal",), _phase_ok(report, "signal")),
        "error_msg": _err(report, ("signal",)),
    })

    # Step 3: Agent -> Backend (WebSocket response) — same signal phase, 0ms extra
    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "agent", "to_node": "backend",
        "protocol": "WebSocket", "direction": "left",
        "action": t("timeline.signaling_response"),
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": "",
        "latency_ms": 0, "latency_pct": 0,
        "status": _status(report, ("signal",), _phase_ok(report, "signal")),
        "error_msg": _err(report, ("signal",)),
    })

    # Step 4: Browser <- Agent (WebRTC ICE)
    idx += 1
    ice_ms = _pos(report.duration_ice)
    steps.append({
        "step_index": idx, "key": "ice", "from_node": "browser", "to_node": "agent",
        "protocol": "WebRTC ICE", "direction": "left",
        "action": t("timeline.ice_dtls"),
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": report.client_ip,
        "latency_ms": ice_ms, "latency_pct": round(ice_ms / total * 100, 1),
        "status": _status(report, ("ice",), _phase_ok(report, "ice")),
        "error_msg": _err(report, ("ice",)),
    })

    # Step 5: Browser <- Agent (WebRTC DataChannel)
    idx += 1
    dc_ms = _pos(report.duration_dc)
    steps.append({
        "step_index": idx, "key": "dc", "from_node": "browser", "to_node": "agent",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_open"),
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": report.client_ip,
        "latency_ms": dc_ms, "latency_pct": round(dc_ms / total * 100, 1),
        "status": _status(report, ("dc",), _phase_ok(report, "dc")),
        "error_msg": _err(report, ("dc",)),
    })

    # Step 6: Agent -> Target (TCP)
    idx += 1
    tcp_ms = _pos(report.agent_tcp_ms)
    steps.append({
        "step_index": idx, "key": "agent_tcp", "from_node": "agent", "to_node": "target",
        "protocol": "TCP", "direction": "right",
        "action": f"{t('timeline.tcp_connect')} ({report.host}:{report.port})",
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": tcp_ms, "latency_pct": round(tcp_ms / total * 100, 1),
        "status": _status(report, ("agent_tcp",), _phase_ok(report, "agent_tcp")),
        "error_msg": _err(report, ("agent_tcp",)),
    })

    # Step 7: Agent -> Target (SSH handshake)
    idx += 1
    ssh_ms = _pos(report.agent_ssh_ms)
    steps.append({
        "step_index": idx, "key": "agent_ssh", "from_node": "agent", "to_node": "target",
        "protocol": "SSH", "direction": "right",
        "action": t("timeline.ssh_handshake"),
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": ssh_ms, "latency_pct": round(ssh_ms / total * 100, 1),
        "status": _status(report, ("agent_ssh",), _phase_ok(report, "agent_ssh")),
        "error_msg": _err(report, ("agent_ssh",)),
    })

    # Step 8: Agent <- Target (Shell启动 + 会话建立)
    idx += 1
    shell_ms = _pos(report.agent_shell_ms)
    steps.append({
        "step_index": idx, "key": "agent_shell", "from_node": "agent", "to_node": "target",
        "protocol": "SSH", "direction": "left",
        "action": t("timeline.shell_start"),
        "src_ip": f"{report.host}:{report.port}", "dst_ip": report.agent_1_ip or report.agent_2_ip or "",
        "latency_ms": shell_ms, "latency_pct": round(shell_ms / total * 100, 1),
        "status": _status(report, ("first_byte",), _phase_ok(report, "agent_shell")),
        "error_msg": _err(report, ("first_byte",)),
    })

    # Step 9: Agent -> Browser (DataChannel首字节回传 — 闭环)
    idx += 1
    data_ms = _pos(report.duration_data)
    steps.append({
        "step_index": idx, "key": "loopback", "from_node": "agent", "to_node": "browser",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_first_byte"),
        "src_ip": report.agent_1_ip or report.agent_2_ip or "", "dst_ip": report.client_ip,
        "latency_ms": data_ms, "latency_pct": round(data_ms / total * 100, 1),
        "status": _loopback_status(report),
        "error_msg": _err(report, ("dc",)),
    })

    return steps


def _compute_gateway_ssh_steps(report: TimelineReport) -> list:
    total = max(report.duration_total, 1)
    steps = []
    idx = 0
    agent_ip = report.agent_2_ip or report.agent_1_ip or ""

    # Step 1: Browser -> Gateway (WebSocket) — 浏览器直连网关 wss，非后端
    idx += 1
    ws_ms = _pos(report.duration_ws)
    steps.append({
        "step_index": idx, "key": "ws", "from_node": "browser", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.connect_gateway_request"),
        "src_ip": report.client_ip, "dst_ip": report.gateway_ip,
        "latency_ms": ws_ms, "latency_pct": round(ws_ms / total * 100, 1),
        "status": _status(report, ("ws",), _phase_ok(report, "ws")),
        "error_msg": _err(report, ("ws",)),
    })

    # Steps 2-5: signaling path — full window on first hop, 0 on hops that carry no measured time
    sig_ms = _pos(report.duration_signal)
    sig_status = _status(report, ("signal",), _phase_ok(report, "signal"))
    sig_err = _err(report, ("signal",))

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal_fwd", "from_node": "backend", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.forward_to_gateway"),
        "src_ip": "", "dst_ip": report.gateway_ip,
        "latency_ms": sig_ms, "latency_pct": round(sig_ms / total * 100, 1),
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "gateway", "to_node": "agent",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.gateway_forward_agent"),
        "src_ip": report.gateway_ip, "dst_ip": agent_ip,
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "agent", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.agent_signaling"),
        "src_ip": agent_ip, "dst_ip": report.gateway_ip,
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "gateway", "to_node": "backend",
        "protocol": "WebSocket", "direction": "left",
        "action": t("timeline.gateway_signaling"),
        "src_ip": report.gateway_ip, "dst_ip": "",
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    # Step 6: Gateway <-> Agent (WebRTC ICE) - 浏览器网关模式无 PeerConnection
    idx += 1
    ice_ms = _pos(report.duration_ice)
    steps.append({
        "step_index": idx, "key": "ice", "from_node": "gateway", "to_node": "agent",
        "protocol": "WebRTC ICE", "direction": "right",
        "action": t("timeline.ice_dtls_p2p"),
        "src_ip": report.gateway_ip, "dst_ip": agent_ip,
        "latency_ms": ice_ms, "latency_pct": round(ice_ms / total * 100, 1),
        "status": _status(report, ("ice",), _phase_ok(report, "ice")),
        "error_msg": _err(report, ("ice",)),
    })

    # Step 7: Gateway <-> Agent (WebRTC DataChannel)
    idx += 1
    dc_ms = _pos(report.duration_dc)
    steps.append({
        "step_index": idx, "key": "dc", "from_node": "gateway", "to_node": "agent",
        "protocol": "WebRTC DC", "direction": "right",
        "action": t("timeline.dc_open"),
        "src_ip": report.gateway_ip, "dst_ip": agent_ip,
        "latency_ms": dc_ms, "latency_pct": round(dc_ms / total * 100, 1),
        "status": _status(report, ("dc",), _phase_ok(report, "dc")),
        "error_msg": _err(report, ("dc",)),
    })

    # Step 8: Agent -> Target (TCP)
    idx += 1
    tcp_ms = _pos(report.agent_tcp_ms)
    steps.append({
        "step_index": idx, "key": "agent_tcp", "from_node": "agent", "to_node": "target",
        "protocol": "TCP", "direction": "right",
        "action": f"{t('timeline.tcp_connect')} ({report.host}:{report.port})",
        "src_ip": agent_ip, "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": tcp_ms, "latency_pct": round(tcp_ms / total * 100, 1),
        "status": _status(report, ("agent_tcp",), _phase_ok(report, "agent_tcp")),
        "error_msg": _err(report, ("agent_tcp",)),
    })

    # Step 9: Agent -> Target (SSH handshake)
    idx += 1
    ssh_ms = _pos(report.agent_ssh_ms)
    steps.append({
        "step_index": idx, "key": "agent_ssh", "from_node": "agent", "to_node": "target",
        "protocol": "SSH", "direction": "right",
        "action": t("timeline.ssh_handshake"),
        "src_ip": agent_ip, "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": ssh_ms, "latency_pct": round(ssh_ms / total * 100, 1),
        "status": _status(report, ("agent_ssh",), _phase_ok(report, "agent_ssh")),
        "error_msg": _err(report, ("agent_ssh",)),
    })

    # Step 10: Agent <- Target (Shell启动 + 会话建立)
    idx += 1
    shell_ms = _pos(report.agent_shell_ms)
    steps.append({
        "step_index": idx, "key": "agent_shell", "from_node": "agent", "to_node": "target",
        "protocol": "SSH", "direction": "left",
        "action": t("timeline.shell_start"),
        "src_ip": f"{report.host}:{report.port}", "dst_ip": agent_ip,
        "latency_ms": shell_ms, "latency_pct": round(shell_ms / total * 100, 1),
        "status": _status(report, ("first_byte",), _phase_ok(report, "agent_shell")),
        "error_msg": _err(report, ("first_byte",)),
    })

    # Step 11: Agent -> Browser (DataChannel首字节回传 — 闭环)
    idx += 1
    data_ms = _pos(report.duration_data)
    steps.append({
        "step_index": idx, "key": "loopback", "from_node": "agent", "to_node": "browser",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_first_byte"),
        "src_ip": agent_ip, "dst_ip": report.client_ip,
        "latency_ms": data_ms, "latency_pct": round(data_ms / total * 100, 1),
        "status": _loopback_status(report),
        "error_msg": _err(report, ("dc",)),
    })

    return steps


def _compute_vnc_rdp_steps(report: TimelineReport) -> list:
    total = max(report.duration_total, 1)
    steps = []
    idx = 0

    # Step 1: Browser -> Backend (WebSocket)
    idx += 1
    ws_ms = _pos(report.duration_ws)
    steps.append({
        "step_index": idx, "key": "ws", "from_node": "browser", "to_node": "backend",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.connect_request"),
        "src_ip": report.client_ip, "dst_ip": "",
        "latency_ms": ws_ms, "latency_pct": round(ws_ms / total * 100, 1),
        "status": _status(report, ("ws",), _phase_ok(report, "ws")),
        "error_msg": _err(report, ("ws",)),
    })

    # Step 2: Browser <-> Agent (WebRTC ICE)
    idx += 1
    ice_ms = _pos(report.duration_ice)
    steps.append({
        "step_index": idx, "key": "ice", "from_node": "browser", "to_node": "agent",
        "protocol": "WebRTC", "direction": "left",
        "action": t("timeline.p2p_establish"),
        "src_ip": report.agent_2_ip or report.agent_1_ip or "", "dst_ip": report.client_ip,
        "latency_ms": ice_ms, "latency_pct": round(ice_ms / total * 100, 1),
        "status": _status(report, ("ice",), _phase_ok(report, "ice")),
        "error_msg": _err(report, ("ice",)),
    })

    # Step 3: Browser <-> Agent (DataChannel)
    idx += 1
    dc_ms = _pos(report.duration_dc)
    steps.append({
        "step_index": idx, "key": "dc", "from_node": "browser", "to_node": "agent",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_open"),
        "src_ip": report.agent_2_ip or report.agent_1_ip or "", "dst_ip": report.client_ip,
        "latency_ms": dc_ms, "latency_pct": round(dc_ms / total * 100, 1),
        "status": _status(report, ("dc",), _phase_ok(report, "dc")),
        "error_msg": _err(report, ("dc",)),
    })

    # Step 4: Agent -> Target (连接)
    idx += 1
    tcp_ms = _pos(report.agent_tcp_ms)
    steps.append({
        "step_index": idx, "key": "agent_tcp", "from_node": "agent", "to_node": "target",
        "protocol": report.conn_type.upper(), "direction": "right",
        "action": f"{t('timeline.connect_target')} ({report.host}:{report.port})",
        "src_ip": report.agent_2_ip or report.agent_1_ip or "", "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": tcp_ms, "latency_pct": round(tcp_ms / total * 100, 1),
        "status": _status(report, ("agent_tcp",), _phase_ok(report, "agent_tcp")),
        "error_msg": _err(report, ("agent_tcp",)),
    })

    # Step 5: Agent -> Browser (DataChannel首字节回传 — 闭环)
    # 目标未连时闭环仅 partial，不得单独支撑 success=1（VNC 成功以 agent 目标连接证据为准）
    idx += 1
    data_ms = _pos(report.duration_data)
    steps.append({
        "step_index": idx, "key": "loopback", "from_node": "agent", "to_node": "browser",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_first_byte"),
        "src_ip": report.agent_2_ip or report.agent_1_ip or "", "dst_ip": report.client_ip,
        "latency_ms": data_ms, "latency_pct": round(data_ms / total * 100, 1),
        "status": _loopback_status(report, target_ok=_target_ok(report)),
        "error_msg": _err(report, ("dc",)),
    })

    return steps


def _compute_gateway_vnc_steps(report: TimelineReport) -> list:
    total = max(report.duration_total, 1)
    steps = []
    idx = 0
    agent_ip = report.agent_2_ip or report.agent_1_ip or ""

    # Step 1: Browser -> Gateway (WebSocket) — 浏览器直连网关 wss，非后端
    idx += 1
    ws_ms = _pos(report.duration_ws)
    steps.append({
        "step_index": idx, "key": "ws", "from_node": "browser", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.connect_gateway_request"),
        "src_ip": report.client_ip, "dst_ip": report.gateway_ip,
        "latency_ms": ws_ms, "latency_pct": round(ws_ms / total * 100, 1),
        "status": _status(report, ("ws",), _phase_ok(report, "ws")),
        "error_msg": _err(report, ("ws",)),
    })

    # Steps 2-5: signaling — full window on first hop, 0 on hops with no measured time
    sig_ms = _pos(report.duration_signal)
    sig_status = _status(report, ("signal",), _phase_ok(report, "signal"))
    sig_err = _err(report, ("signal",))

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal_fwd", "from_node": "backend", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.forward_to_gateway"),
        "src_ip": "", "dst_ip": report.gateway_ip,
        "latency_ms": sig_ms, "latency_pct": round(sig_ms / total * 100, 1),
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "gateway", "to_node": "agent",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.gateway_forward_agent"),
        "src_ip": report.gateway_ip, "dst_ip": agent_ip,
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "agent", "to_node": "gateway",
        "protocol": "WebSocket", "direction": "right",
        "action": t("timeline.agent_signaling"),
        "src_ip": agent_ip, "dst_ip": report.gateway_ip,
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    idx += 1
    steps.append({
        "step_index": idx, "key": "signal", "from_node": "gateway", "to_node": "backend",
        "protocol": "WebSocket", "direction": "left",
        "action": t("timeline.gateway_signaling"),
        "src_ip": report.gateway_ip, "dst_ip": "",
        "latency_ms": 0, "latency_pct": 0,
        "status": sig_status, "error_msg": sig_err,
    })

    # Step 6: Gateway <-> Agent (WebRTC ICE/DC) — 浏览器网关模式无 PeerConnection
    idx += 1
    ice_ms = _pos(report.duration_ice)
    dc_ms = _pos(report.duration_dc)
    webrtc_ms = ice_ms + dc_ms
    steps.append({
        "step_index": idx, "key": "ice_dc", "from_node": "gateway", "to_node": "agent",
        "protocol": "WebRTC", "direction": "right",
        "action": t("timeline.p2p_ice_dc"),
        "src_ip": report.gateway_ip, "dst_ip": agent_ip,
        "latency_ms": webrtc_ms, "latency_pct": round(webrtc_ms / total * 100, 1),
        "status": _status(report, ("ice", "dc"), _phase_ok(report, "ice") or _phase_ok(report, "dc")),
        "error_msg": _err(report, ("ice", "dc")),
    })

    # Step 7: Agent -> Target (VNC/TCP连接)
    idx += 1
    tcp_ms = _pos(report.agent_tcp_ms)
    steps.append({
        "step_index": idx, "key": "agent_tcp", "from_node": "agent", "to_node": "target",
        "protocol": report.conn_type.upper(), "direction": "right",
        "action": f"{t('timeline.connect_target')} ({report.host}:{report.port})",
        "src_ip": agent_ip, "dst_ip": f"{report.host}:{report.port}",
        "latency_ms": tcp_ms, "latency_pct": round(tcp_ms / total * 100, 1),
        "status": _status(report, ("agent_tcp",), _phase_ok(report, "agent_tcp")),
        "error_msg": _err(report, ("agent_tcp",)),
    })

    # Step 8: Agent -> Browser (DataChannel首字节回传 — 闭环)
    # 目标未连时闭环仅 partial，不得单独支撑 success=1（VNC 成功以 agent 目标连接证据为准）
    idx += 1
    data_ms = _pos(report.duration_data)
    steps.append({
        "step_index": idx, "key": "loopback", "from_node": "agent", "to_node": "browser",
        "protocol": "WebRTC DC", "direction": "left",
        "action": t("timeline.dc_first_byte"),
        "src_ip": agent_ip, "dst_ip": report.client_ip,
        "latency_ms": data_ms, "latency_pct": round(data_ms / total * 100, 1),
        "status": _loopback_status(report, target_ok=_target_ok(report)),
        "error_msg": _err(report, ("dc",)),
    })

    return steps


def _finalize_steps(steps: list, report: TimelineReport) -> list:
    """Normalize latency_pct and mark the failed step from error_stage."""
    stage_sum = sum(max(getattr(report, f) or 0, 0) for f in (
        "duration_ws", "duration_signal", "duration_ice", "duration_dc", "duration_data"))
    agent_sum = report.agent_connect_ms or sum(
        max(getattr(report, f) or 0, 0) for f in ("agent_tcp_ms", "agent_ssh_ms", "agent_shell_ms"))
    denom = max(report.duration_total or stage_sum or agent_sum or 1, 1)

    for s in steps:
        s["latency_pct"] = round(min(max(s["latency_ms"] or 0, 0) / denom * 100, 100), 1)

    if report.error_stage or report.error_msg:
        if not any(s["status"] == "failed" for s in steps):
            last_ok = -1
            for i, s in enumerate(steps):
                if s["status"] == "ok":
                    last_ok = i
            target = next((s for s in steps[last_ok + 1:] if s["status"] != "ok"), None)
            if target is None:
                target = next((s for s in steps if s["status"] == "skipped"), None)
            if target is not None:
                target["status"] = "failed"
                if not target.get("error_msg"):
                    target["error_msg"] = report.error_msg or report.error_stage or "failed"
    return steps


# 步骤绝对时刻窗口（相对 t_start 的毫秒偏移）；仅浏览器侧阶段有可测量的时刻，
# agent 阶段（tcp/ssh/shell）无浏览器侧时间戳 → 保持 0/0，不编造。
_STEP_WINDOWS = {
    "ws": ("t_start", "t_ws_open"),
    "signal_fwd": ("t_ws_open", "t_signal_ok"),
    "ice": ("t_signal_ok", "t_rtc_connected"),
    "ice_dc": ("t_signal_ok", "t_dc_open"),
    "dc": ("t_rtc_connected", "t_dc_open"),
    "loopback": ("t_dc_open", "t_first_data"),
}


def _apply_step_times(steps: list, report: TimelineReport) -> list:
    base = _pos(report.t_start)
    for s in steps:
        win = _STEP_WINDOWS.get(s.get("key") or "")
        if not win or base <= 0:
            s["t_start"] = 0
            s["t_end"] = 0
            continue
        end = _pos(getattr(report, win[1], 0))
        if end <= 0:
            s["t_start"] = 0
            s["t_end"] = 0
            continue
        t0 = 0.0 if win[0] == "t_start" else max(_pos(getattr(report, win[0], 0)) - base, 0)
        t1 = max(end - base, 0)
        s["t_start"] = round(t0, 2)
        s["t_end"] = round(max(t0, t1), 2)
    return steps


def _compute_steps(report: TimelineReport) -> list:
    if report.path_mode == "gateway" and report.gateway_ip:
        if report.conn_type == "ssh":
            steps = _compute_gateway_ssh_steps(report)
        else:
            steps = _compute_gateway_vnc_steps(report)
    elif report.conn_type == "ssh":
        steps = _compute_direct_ssh_steps(report)
    else:
        steps = _compute_vnc_rdp_steps(report)
    steps = _finalize_steps(steps, report)
    return _apply_step_times(steps, report)


# ── API Endpoints ──

# connection_timeline 列宽（超长触发 DataError 1406）；仅 TimelineReport 已有字段
_TIMELINE_STR_LIMITS = {
    "room_id": 128, "conn_name": 255, "conn_type": 32,
    "host": 255, "username": 255, "path_mode": 32, "client_ip": 64,
    "agent_1_ip": 128, "agent_1_name": 255, "agent_2_ip": 128, "agent_2_name": 255,
    "gateway_ip": 128, "agent_id": 64, "error_stage": 32,
    "browser": 32, "os_info": 64, "connected_at": 64,
}


def _trunc(val, limit: int) -> str:
    if val is None:
        return ""
    s = str(val)
    return s[:limit]


def _clamp_report(req: TimelineReport) -> TimelineReport:
    model_fields = getattr(type(req), "model_fields", {}) or {}
    for field, limit in _TIMELINE_STR_LIMITS.items():
        if field in model_fields:
            setattr(req, field, _trunc(getattr(req, field, None), limit))
    # error_msg 是 TEXT，仍防异常巨型串
    req.error_msg = _trunc(req.error_msg, 4000)
    req.room_id = req.room_id or f"anon-{datetime.now().strftime('%Y%m%d%H%M%S%f')}"
    # 数值钳制：dc.onopen 可早于 connectionstatechange → duration_dc 为负（如 -16.2ms）
    for field in ("duration_total", "duration_ws", "duration_signal", "duration_ice",
                  "duration_dc", "duration_data", "agent_tcp_ms", "agent_ssh_ms",
                  "agent_shell_ms", "agent_connect_ms",
                  "t_start", "t_ws_open", "t_signal_ok", "t_rtc_connected",
                  "t_dc_open", "t_first_data", "t_error"):
        if field in model_fields:
            setattr(req, field, _pos(getattr(req, field, 0)))
    req.agent_ok = 1 if (req.agent_ok or 0) else 0
    return req


def _judge_report(req: TimelineReport) -> tuple[int, str, str]:
    """派生 (success, error_stage, error_msg)。
    必须在 _compute_steps 之前调用并写回 error_stage：否则 failed 步骤标记、
    completed 统计与 failure_analysis 全部基于未派生的 error_stage 而错位。"""
    if req.error_stage or req.error_msg:
        return 0, req.error_stage or "", req.error_msg or ""

    has_tcp = _pos(req.agent_tcp_ms) > 0
    has_ssh = _pos(req.agent_ssh_ms) > 0
    has_shell = _pos(req.agent_shell_ms) > 0
    # agent 成功诊断已到达（error 分支已在上面返回，此处必无 error）：
    # agent_ok 标志（前端置位）或 agent_connect_ms>0，或 host 已回传
    # （agent 错误诊断必带 error_stage，见 wragent signal.go reportVNCError/reportAgentError）
    diag_ok = _agent_evidence(req) or bool(req.agent_ssh_host)

    if req.conn_type == "ssh":
        # SSH: 需要 agent 侧时序数据证明连接成功
        # agent_connect_ms = 全链路 (TCP+SSH+Shell) 时长为权威信号
        if diag_ok or has_shell:
            return 1, "", ""
        if has_tcp and has_ssh:
            return 1, "", ""
        if has_tcp or has_ssh:
            return 1, "", ""  # 池复用: 单字段足以证明连接成功
        if _loopback_evidence(req):
            return 1, "", ""  # 闭环ok = 数据已流通 = 连接成功
        # agent 时序缺失 = 无法确认成功；注入首个 agent 阶段供 failed 步骤标记
        return 0, "agent_tcp", "Agent未上报连接时序，无法确认连接成功"
    # VNC/RDP: 必须有 agent→目标 连接证据；仅 DC 首字节闭环不算成功
    if has_tcp or diag_ok:
        return 1, "", ""
    return 0, "agent_tcp", "Agent未连接目标服务器"


@timeline_router.post("/report")
async def report_timeline(req: TimelineReport, request: Request, db: AsyncSession = Depends(get_db), user=Depends(get_current_user)):
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    _clamp_report(req)

    # Concurrent reports for the same room_id:
    # - INSERT can hit UNIQUE(room_id) → IntegrityError
    # - two UPDATEs of one row can hit MariaDB 1020 "Record has changed" → OperationalError
    # - overlong VARCHAR → DataError 1406 (clamped above; still retry once if hit)
    last_exc: Exception | None = None
    for attempt in range(3):
        try:
            return await _upsert_timeline(req, db, user, now)
        except (IntegrityError, OperationalError, DataError) as e:
            last_exc = e
            await db.rollback()
            if attempt == 2:
                raise
    if last_exc:
        raise last_exc
    raise HTTPException(status_code=500, detail="timeline report failed")


async def _upsert_timeline(req: TimelineReport, db: AsyncSession, user: dict, now: str):
    # 行锁串行化同 room_id 并发 upsert，避免 MariaDB 1020 "Record has changed"
    q = (
        select(ConnectionTimeline)
        .where(ConnectionTimeline.room_id == req.room_id)
        .with_for_update()
    )
    result = await db.execute(q)
    record = result.scalar_one_or_none()

    # 先判定 success/派生 error_stage，再计算 steps：
    # failed 步骤标记、completed 统计、failure_analysis 都依赖派生后的 error_stage
    success, eff_stage, eff_msg = _judge_report(req)
    req.error_stage = eff_stage
    req.error_msg = eff_msg
    steps = _compute_steps(req)
    completed = sum(1 for s in steps if s["status"] == "ok")
    total = len(steps)
    failed_step_no = next((s["step_index"] for s in steps if s["status"] == "failed"), 0)

    if record:
        # Update fields
        updates = {}
        # Always-update fields (can be cleared)
        for field in ["error_stage", "error_msg", "connected_at"]:
            updates[field] = getattr(req, field, None) or ""
        # path_mode: trust latest non-empty report (browser finalizes gateway/direct)
        if req.path_mode and req.path_mode in ("direct", "gateway"):
            updates["path_mode"] = req.path_mode
        # Non-empty-only fields
        for field in ["duration_total", "client_ip", "agent_1_ip", "agent_1_name",
                       "agent_2_ip", "agent_2_name", "gateway_ip", "browser", "os_info",
                       "agent_ssh_host", "agent_ssh_port"]:
            val = getattr(req, field, None)
            if val and val != "" and val != 0:
                updates[field] = val
        # Always overwrite failed_step from computed steps (0 clears stale value)
        updates["failed_step"] = failed_step_no
        updates["completed_steps"] = completed
        updates["total_steps"] = total
        # Success/error_stage 由 _judge_report 统一判定（含 VNC 无证据注入）
        updates["success"] = success
        updates["error_stage"] = eff_stage
        updates["error_msg"] = eff_msg

        updates["updated_at"] = now
        for k, v in updates.items():
            setattr(record, k, v)
    else:
        record = ConnectionTimeline(
            room_id=req.room_id,
            user_id=user["id"],
            conn_name=req.conn_name,
            conn_type=req.conn_type,
            host=req.host,
            port=req.port,
            username=req.username,
            path_mode=req.path_mode,
            client_ip=req.client_ip,
            agent_1_ip=req.agent_1_ip,
            agent_1_name=req.agent_1_name,
            agent_2_ip=req.agent_2_ip,
            agent_2_name=req.agent_2_name,
            gateway_ip=req.gateway_ip,
            agent_id=req.agent_id,
            duration_total=req.duration_total,
            success=success,
            error_stage=eff_stage,
            error_msg=eff_msg,
            failed_step=failed_step_no,
            completed_steps=completed,
            total_steps=total,
            browser=req.browser,
            os_info=req.os_info,
            connected_at=req.connected_at,
            created_at=now,
            updated_at=now,
        )
        db.add(record)

    # Upsert steps (delete old, insert new) — 同事务，持主记录锁期间完成
    old_steps = await db.execute(
        select(TimelineStep)
        .where(TimelineStep.room_id == req.room_id)
        .with_for_update()
    )
    for s in old_steps.scalars().all():
        await db.delete(s)

    for s in steps:
        db.add(TimelineStep(
            room_id=_trunc(req.room_id, 128),
            step_index=s["step_index"],
            from_node=_trunc(s["from_node"], 32),
            to_node=_trunc(s["to_node"], 32),
            protocol=_trunc(s["protocol"], 32),
            direction=_trunc(s.get("direction") or "right", 16),
            action=_trunc(s["action"], 255),
            src_ip=_trunc(s["src_ip"], 128),
            dst_ip=_trunc(s["dst_ip"], 128),
            latency_ms=s["latency_ms"],
            latency_pct=s["latency_pct"],
            t_start=s.get("t_start") or 0,
            t_end=s.get("t_end") or 0,
            status=_trunc(s["status"], 16),
            error_msg=_trunc(s["error_msg"], 500),
        ))

    await db.commit()
    return {"ok": True, "room_id": req.room_id, "steps": len(steps)}


@timeline_router.post("/records")
async def list_records(req: TimelineQuery, db: AsyncSession = Depends(get_db), user=Depends(get_current_user)):
    conditions = [ConnectionTimeline.user_id == user["id"]]
    if req.conn_type:
        conditions.append(ConnectionTimeline.conn_type == req.conn_type)
    if req.success is not None:
        conditions.append(ConnectionTimeline.success == req.success)
    if req.agent_id:
        conditions.append(ConnectionTimeline.agent_id == req.agent_id)
    if req.start_date:
        conditions.append(ConnectionTimeline.created_at >= req.start_date)
    if req.end_date:
        conditions.append(ConnectionTimeline.created_at <= req.end_date + " 23:59:59")
    if req.search:
        conditions.append(ConnectionTimeline.conn_name.contains(req.search) | ConnectionTimeline.host.contains(req.search))

    where = and_(*conditions) if conditions else True
    total_q = await db.execute(select(func.count(ConnectionTimeline.id)).where(where))
    total = total_q.scalar() or 0

    q = (select(ConnectionTimeline).where(where)
         .order_by(desc(ConnectionTimeline.created_at))
         .offset((req.page - 1) * req.page_size).limit(req.page_size))
    result = await db.execute(q)
    items = []
    for r in result.scalars().all():
        items.append({
            "id": r.id, "room_id": r.room_id, "conn_name": r.conn_name,
            "conn_type": r.conn_type, "host": r.host, "port": r.port,
            "path_mode": r.path_mode, "duration_total": r.duration_total,
            "success": r.success, "error_stage": r.error_stage,
            "completed_steps": r.completed_steps, "total_steps": r.total_steps,
            "agent_id": r.agent_id, "created_at": r.created_at,
            "browser": r.browser or "", "gateway_ip": r.gateway_ip or "", 
        })

    return {"items": items, "total": total, "page": req.page, "page_size": req.page_size}


@timeline_router.post("/detail")
async def get_detail(req: TimelineDetailQuery, db: AsyncSession = Depends(get_db), user=Depends(get_current_user)):
    conditions = [ConnectionTimeline.user_id == user["id"]]
    if req.record_id:
        conditions.append(ConnectionTimeline.id == req.record_id)
    elif req.room_id:
        conditions.append(ConnectionTimeline.room_id == req.room_id)
    else:
        raise HTTPException(status_code=400, detail="room_id or record_id required")

    result = await db.execute(select(ConnectionTimeline).where(and_(*conditions)))
    record = result.scalar_one_or_none()
    if not record:
        raise HTTPException(status_code=404, detail="not found")

    # Get steps
    steps_q = await db.execute(
        select(TimelineStep).where(TimelineStep.room_id == record.room_id)
        .order_by(TimelineStep.step_index)
    )
    steps = []
    for s in steps_q.scalars().all():
        steps.append({
            "index": s.step_index, "from_node": s.from_node, "to_node": s.to_node,
            "protocol": s.protocol, "direction": s.direction, "action": s.action,
            "src_ip": s.src_ip, "dst_ip": s.dst_ip,
            "latency_ms": s.latency_ms, "latency_pct": s.latency_pct,
            "t_start": s.t_start or 0, "t_end": s.t_end or 0,
            "status": s.status, "error_msg": s.error_msg,
        })

    # Build actors list — backend has no public IP; never invent one from step labels
    actors = [
        {"id": "browser", "name": t("timeline.browser"), "ip": record.client_ip or "", "color": "#3b82f6"},
        {"id": "backend", "name": "Backend", "ip": "", "color": "#6366f1"},
    ]
    if record.path_mode == "gateway" and record.gateway_ip:
        actors.append({"id": "gateway", "name": "wrgateway", "ip": record.gateway_ip or "", "color": "#8b5cf6"})
    agent_ip = record.agent_2_ip or record.agent_1_ip or ""
    agent_name = record.agent_2_name or record.agent_1_name or "wrAgent"
    actors.append({"id": "agent", "name": agent_name, "ip": agent_ip, "color": "#10b981"})
    actors.append({"id": "target", "name": t("timeline.target_server"), "ip": f"{record.host}:{record.port}", "color": "#f59e0b"})

    # Failure analysis — derive from steps, not the often-zero failed_step column
    failure_analysis = None
    if not record.success:
        failed_step = next((s for s in steps if s["status"] == "failed"), None) \
            or next((s for s in steps if s["status"] == "partial"), None)
        failed_idx = (failed_step["index"] if failed_step else None) or record.failed_step or 0
        failure_analysis = {
            "failed_step": failed_idx,
            "failed_node": (failed_step or {}).get("from_node", ""),
            "failed_protocol": (failed_step or {}).get("protocol", ""),
            "root_cause": record.error_msg or record.error_stage or "连接失败",
            "completed_steps": record.completed_steps,
            "total_steps": record.total_steps,
        }

    return {
        "connection": {
            "id": record.id, "room_id": record.room_id,
            "conn_name": record.conn_name, "conn_type": record.conn_type,
            "host": record.host, "port": record.port,
            "path_mode": record.path_mode,
            "client_ip": record.client_ip,
            "agent_ip": agent_ip, "agent_name": agent_name,
            "gateway_ip": record.gateway_ip,
            "duration_total": record.duration_total,
            "success": record.success,
            "error_stage": record.error_stage, "error_msg": record.error_msg,
            "completed_steps": record.completed_steps, "total_steps": record.total_steps,
            "connected_at": record.connected_at, "created_at": record.created_at,
        },
        "steps": steps,
        "actors": actors,
        "failure_analysis": failure_analysis,
    }


@timeline_router.post("/stats")
async def get_stats(req: TimelineQuery, db: AsyncSession = Depends(get_db), user=Depends(get_current_user)):
    conditions = [ConnectionTimeline.user_id == user["id"]]
    if req.conn_type:
        conditions.append(ConnectionTimeline.conn_type == req.conn_type)
    if req.start_date:
        conditions.append(ConnectionTimeline.created_at >= req.start_date)
    if req.end_date:
        conditions.append(ConnectionTimeline.created_at <= req.end_date + " 23:59:59")

    where = and_(*conditions) if conditions else True

    total_q = await db.execute(select(func.count(ConnectionTimeline.id)).where(where))
    total = total_q.scalar() or 0

    success_q = await db.execute(select(func.count(ConnectionTimeline.id)).where(and_(where, ConnectionTimeline.success == 1)))
    success_count = success_q.scalar() or 0

    avg_q = await db.execute(select(func.avg(ConnectionTimeline.duration_total)).where(and_(where, ConnectionTimeline.success == 1)))
    avg_total = round(avg_q.scalar() or 0, 1)

    return {
        "total": total,
        "success_count": success_count,
        "fail_count": total - success_count,
        "success_rate": round(success_count / max(total, 1) * 100, 1),
        "avg_total": avg_total,
    }
