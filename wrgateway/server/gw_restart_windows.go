//go:build windows

package server

import (
	"log"
	"os"
	"os/exec"
)

// restartSelf 升级完成后重启自身（Windows：通过子进程拉起新进程后退出）
func restartSelf() {
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("[GW-UPGRADE] 重启失败: %v，需手动重启", err)
	}
	os.Exit(0)
}