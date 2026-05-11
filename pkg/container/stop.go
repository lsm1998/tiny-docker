package container

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"tinydocker/cgroups"
	"tinydocker/pkg/network"
	"tinydocker/pkg/system"
)

const stopGracePeriod = 10 * time.Second

// Stop 根据容器id停止
func Stop(id string) error {
	dir, cfg, err := FindContainerDir(id)
	if err != nil {
		return err
	}
	if cfg.NetworkMode == NetworkBridge {
		if err := system.RequireRoot("stop"); err != nil {
			return err
		}
	}

	// 持锁保护 read-modify-write
	return withContainerLocked(dir, func() error {
		// 重新读取，避免 lock 之前的状态已被并发修改
		cfg, err := ReadConfig(dir)
		if err != nil {
			return err
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
			return err
		}

		if cfg.NetworkMode == NetworkBridge {
			if e := network.ReleaseEndpoint(cfg.ID); e != nil {
				fmt.Fprintf(os.Stderr, "warn: release endpoint: %s\n", e)
			}
		}
		if e := cgroups.RemoveLeaf(cfg.ID); e != nil {
			fmt.Fprintf(os.Stderr, "warn: remove cgroup: %s\n", e)
		}

		cfg.Status = "exited"
		cfg.ExitCode = exitCode
		return writeConfig(dir, cfg)
	})
}

func stopProcess(proc *os.Process, timeout time.Duration) (int, error) {
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return 0, nil
		}
		return 0, fmt.Errorf("send SIGTERM: %w", err)
	}

	// 用 Wait4 阻塞等待，比轮询 Signal(0) 更高效
	done := make(chan int, 1)
	go func() {
		pid := proc.Pid
		var ws syscall.WaitStatus
		for {
			wpid, err := syscall.Wait4(pid, &ws, 0, nil)
			if err != nil {
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				done <- -1
				return
			}
			if wpid == pid {
				if ws.Exited() {
					done <- ws.ExitStatus()
				} else if ws.Signaled() {
					done <- 128 + int(ws.Signal())
				} else {
					done <- -1
				}
				return
			}
		}
	}()

	select {
	case code := <-done:
		return code, nil
	case <-time.After(timeout):
		_ = proc.Signal(syscall.SIGKILL)
		// SIGKILL 后同样用 Wait4 阻塞等退出
		select {
		case code := <-done:
			return code, nil
		case <-time.After(3 * time.Second):
			return 137, fmt.Errorf("process %d did not exit after SIGKILL", proc.Pid)
		}
	}
}

// waitGone 短暂等待进程从 /proc 消失
func waitGone(proc *os.Process, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}