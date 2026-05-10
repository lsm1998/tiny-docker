package main

import (
	"fmt"
	"os"
	"path/filepath"
	"tinydocker/cli"
	_ "tinydocker/config"
	"tinydocker/pkg/container"
	"tinydocker/pkg/image"
	"tinydocker/pkg/network"
	"tinydocker/pkg/system"
)

func main() {
	container.MaybeInit()
	container.MaybeRunExecContainer()

	network.GarbageCollect(isContainerAlive)

	if len(os.Args) <= 1 {
		system.Panic("usage: tinydocker <command>")
	}
	manager := cli.NewCliManager()
	if err := manager.Run(os.Args[1:]); err != nil {
		system.Panic("%s", err.Error())
	}
}

// isContainerAlive 判断指定 id 的容器是否仍在运行(供 network.GarbageCollect 使用)
func isContainerAlive(id string) bool {
	containerDir := filepath.Join(image.DataRoot(), "containers", id)
	cfg, err := container.ReadConfig(containerDir)
	if err != nil {
		return false
	}
	if cfg.Status != "running" || cfg.Pid <= 0 {
		return false
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", cfg.Pid)); err != nil {
		return false
	}
	return true
}
