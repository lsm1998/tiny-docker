package network

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// withContainerNetns 锁定当前 OS 线程,切到 pid 的 net namespace 执行 fn,完成后切回
func withContainerNetns(pid int, fn func() error) (retErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNs, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return fmt.Errorf("open current netns: %w", err)
	}
	defer origNs.Close()

	target, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return fmt.Errorf("open container netns: %w", err)
	}
	defer target.Close()

	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("setns container: %w", err)
	}
	defer func() {
		if err := unix.Setns(int(origNs.Fd()), unix.CLONE_NEWNET); err != nil && retErr == nil {
			retErr = fmt.Errorf("setns restore: %w", err)
		}
	}()

	return fn()
}
