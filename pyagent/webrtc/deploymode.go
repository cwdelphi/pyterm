package webrtc

import "os"

// DeployMode 部署形态: "docker"(容器内, 存在 /.dockerenv) / "host"(宿主机/systemd)。
// 可用环境变量 DEPLOY_MODE 显式覆盖(兜底)。
var DeployMode = detectDeployMode("/")

// detectDeployMode 检测部署形态: /.dockerenv 为 Docker 容器 rootfs 标记(pid:host 容器亦存在)。
// root 参数便于测试注入任意前缀目录。
func detectDeployMode(root string) string {
	if v := os.Getenv("DEPLOY_MODE"); v != "" {
		return v
	}
	if _, err := os.Stat(root + "/.dockerenv"); err == nil {
		return "docker"
	}
	return "host"
}
