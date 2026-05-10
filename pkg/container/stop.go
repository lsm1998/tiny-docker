package container

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"tinydocker/pkg/network"
)

const stopGracePeriod = 10 * time.Second

// Stop 根据容器id停止
func Stop(id string) error {
	dir, cfg, err := findContainerDir(id)
	if err != nil {
		return err
	}
	if cfg.NetworkMode == NetworkBridge {
		if err := requireRoot("stop"); err != nil {
			return err
		}
	}
	if cfg.Status != "running" {
		return fmt.Errorf("container %q is not running", id)
	}

	proc, err := os.FindProcess(cfg.Pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", cfg.Pid, err)
	}

	exitCode, err := stopProcess(proc, stopGracePeriod)
	if err != nil {
		// 进程没杀掉(权限/EPERM 等),网络保留,让调用者排查
		return err
	}

	if cfg.NetworkMode == NetworkBridge {
		if e := network.ReleaseEndpoint(cfg.ID); e != nil {
			fmt.Fprintf(os.Stderr, "warn: release endpoint: %s\n", e)
		}
	}

	cfg.Status = "exited"
	cfg.ExitCode = exitCode
	return writeConfig(dir, cfg)
}

func stopProcess(proc *os.Process, timeout time.Duration) (int, error) {
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return 0, nil
		}
		return 0, fmt.Errorf("send SIGTERM: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return 143, nil
		}
		if time.Now().After(deadline) {
			_ = proc.Signal(syscall.SIGKILL)
			// 等一小下让内核完成清理,避免立刻 ReleaseEndpoint 时 netns 还在用
			waitGone(proc, 2*time.Second)
			return 137, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitGone 短暂等待进程从 /proc 消失;超时也直接返回,不阻塞外层流程
func waitGone(proc *os.Process, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
