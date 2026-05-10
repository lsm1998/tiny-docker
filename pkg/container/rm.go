package container

import (
	"fmt"
	"os"
	"syscall"
)

// Remove 根据容器id删除容器
func Remove(id string, force bool) error {
	dir, cfg, err := findContainerDir(id)
	if err != nil {
		return err
	}

	if cfg.Status == "running" {
		if !force {
			return fmt.Errorf("container %q is running, stop it first or use -f", id)
		}
		stopPortmap(dir)
		proc, _ := os.FindProcess(cfg.Pid)
		if proc != nil {
			proc.Signal(syscall.SIGKILL)
		}
	}

	syscall.Unmount(dir+"/merged", syscall.MNT_DETACH)
	return os.RemoveAll(dir)
}
