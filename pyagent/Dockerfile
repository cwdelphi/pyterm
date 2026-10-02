# wr 单二进制运行时镜像 —— 只含基座，不含 Go 二进制
#
# 二进制由宿主 scripts/build-wr.sh 产出 ./wr/wr（及 wr-arm64），compose 以
# bind mount 挂到 /app/pyagent，启动时 `cp /app/wr /usr/local/bin/wr && exec ...`：
#   1) 进程不直接执行 bind mount 的宿主文件，热升级重写 ./wr/wr 时不会 ETXTBSY；
#   2) 换二进制只需重写宿主文件 + `docker compose up -d --force-recreate`，
#      无需重新 build 镜像；
#   3) 三实例（wragent/wragent2/wrgateway）共用同一文件，与单二进制方案一致。
# 因为不做 COPY，buildx 出 linux/amd64 + linux/arm64 也不需要 QEMU。
#
# 基座选型见 docs/wr合并为单二进制_v1.0.md §0：
#   - /bin/sh → 启动包装的 cp/exec、安装脚本管道执行
#   - /usr/bin/nsenter → wr/webrtc/localterm.go 宿主控制台（HOST_CONSOLE=1）
#   - /etc/ssl/certs/ca-certificates.crt → TLS（alpine:3.20 自带 217KB 包）
#   - 时区 TZ=Asia/Shanghai 由 Go 内嵌 time/tzdata 提供（main.go import），不装 tzdata
#   - 本镜像不设 ENTRYPOINT，完整启动命令由 compose 的 command 提供
FROM alpine:3.20
# 网关 WSS 监听（host 网络下由 config 的 listen 决定，此处仅作端口说明）
EXPOSE 5599
CMD ["sh","-c","echo 'pyterm/pyagent: 本镜像不含二进制，需按 docker-compose.yaml bind mount 宿主 ./pyagent/pyagent 到 /app/pyagent' >&2; exit 1"]
