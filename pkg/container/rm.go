package container

import (
	"fmt"
	"os"
	"syscall"

	"tinydocker/cgroups"
	"tinydocker/pkg/network"
)

// Remove 根据容器id删除容器
func Remove(id string, force bool) error {
	dir, cfg, err := FindContainerDir(id)
	if err != nil {
		return err
	}
	if cfg.NetworkMode == NetworkBridge {
		if err := requireRoot("rm"); err != nil {
			return err
		}
	}

	if cfg.Status == "running" {
		if !force {
			return fmt.Errorf("container %q is running, stop it first or use -f", id)
		}
		if err := network.ReleaseEndpoint(cfg.ID); err != nil {
			fmt.Fprintf(os.Stderr, "warn: release endpoint: %s\n", err)
		}
		proc, _ := os.FindProcess(cfg.Pid)
		if proc != nil {
			proc.Signal(syscall.SIGKILL)
		}
	} else {
		// 已退出的容器仍可能有 endpoint 残留(异常退出)
		_ = network.ReleaseEndpoint(cfg.ID)
	}

	if err := cgroups.RemoveLeaf(cfg.ID); err != nil {
		fmt.Fprintf(os.Stderr, "warn: remove cgroup: %s\n", err)
	}

	syscall.Unmount(dir+"/merged", syscall.MNT_DETACH)
	return os.RemoveAll(dir)
}
