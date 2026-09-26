//go:build linux && !windows

package server

import (
	"log"
	"os"
	"syscall"
)

// restartSelf 升级完成后重启自身（POSIX：直接用 exec 替换进程）
func restartSelf() {
	argv := append([]string{os.Args[0]}, os.Args[1:]...)
	env := os.Environ()
	if err := syscall.Exec(os.Args[0], argv, env); err != nil {
		log.Printf("[GW-UPGRADE] 重启失败: %v，需手动重启", err)
	}
}