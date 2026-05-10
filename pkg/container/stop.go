package container

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Stop 根据容器id停止
func Stop(id string) error {
	dir, cfg, err := findContainerDir(id)
	if err != nil {
		return err
	}
	if cfg.Status != "running" {
		return fmt.Errorf("container %q is not running", id)
	}

	stopPortmap(dir)

	proc, err := os.FindProcess(cfg.Pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", cfg.Pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			cfg.Status = "exited"
			cfg.ExitCode = 0
			writeConfig(dir, cfg)
			return nil
		}
		return fmt.Errorf("stop container: %w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			cfg.Status = "exited"
			cfg.ExitCode = 143
			writeConfig(dir, cfg)
			return nil
		}
		if time.Now().After(deadline) {
			proc.Signal(syscall.SIGKILL)
			cfg.Status = "exited"
			cfg.ExitCode = 137
			writeConfig(dir, cfg)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}
