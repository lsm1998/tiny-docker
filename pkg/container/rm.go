package container

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"tinydocker/cgroups"
	"tinydocker/pkg/network"
	"tinydocker/pkg/system"
)

// Remove 根据容器id删除容器
func Remove(id string, force bool) error {
	dir, cfg, err := FindContainerDir(id)
	if err != nil {
		return err
	}
	if cfg.NetworkMode == NetworkBridge {
		if err := system.RequireRoot("rm"); err != nil {
			return err
		}
	}

	// 持锁保护 read-modify-write，防止与并发 stop/rm 竞争
	return withContainerLocked(dir, func() error {
		cfg, err := ReadConfig(dir)
		if err != nil {
			return err
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
				_ = proc.Signal(syscall.SIGKILL)
				waitGone(proc, 5*time.Second)
			}
		} else {
			_ = network.ReleaseEndpoint(cfg.ID)
		}

		if err := cgroups.RemoveLeaf(cfg.ID); err != nil {
			fmt.Fprintf(os.Stderr, "warn: remove cgroup: %s\n", err)
		}

		_ = syscall.Unmount(dir+"/merged", syscall.MNT_DETACH)
		return os.RemoveAll(dir)
	})
}