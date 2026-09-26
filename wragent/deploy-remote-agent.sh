#!/bin/bash
# 远程 Agent 部署（推荐方式：在管理后台该 Agent 的「部署」弹窗复制一键命令）
# 一键命令形如:
#   curl -fsSL "https://<门户>/api/deploy/agent?method=systemd&id=<AGENT_ID>" | bash
#   curl -fsSL "https://<门户>/api/deploy/agent?method=docker&id=<AGENT_ID>" | bash
#
# 脚本自动处理: 下载最新版二进制、非 root 自动 sudo、systemd 服务或 docker 容器拉起。
# 新 Agent 无 token 时启动会进入 setup 模式, 按日志提示打开链接完成注册即可。

set -e

echo "请使用门户管理后台一键部署脚本（如上所示），或手动部署："
echo ""
cat <<'EOF'
# --- 手动部署(systemd, root) ---
mkdir -p /opt/wragent/config /usr/local/bin
curl -fsSL "https://<门户>/api/deploy/wragent/linux-amd64" -o /usr/local/bin/wragent
chmod +x /usr/local/bin/wragent
cat > /opt/wragent/config/config.json <<CONF
{
  "server_url": "wss://<门户>/api/ws/webrtc",
  "agent_id": "<AGENT_ID>",
  "auth_token": "<从管理后台获取的 token>"
}
CONF
# 带 token 直接启动（无需 setup）:
/usr/local/bin/wragent -config /opt/wragent/config/config.json
EOF