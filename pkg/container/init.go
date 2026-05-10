package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const initEnvKey = "_TINYDOCKER_INIT"

type initSpec struct {
	MergedDir  string
	MountOpts  string
	WorkingDir string
	Env        []string
	Argv       []string
	NewNetns   bool
}

func MaybeInit() {
	raw := os.Getenv(initEnvKey)
	if raw == "" {
		return
	}
	os.Unsetenv(initEnvKey)

	var spec initSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker init: parse spec: %s\n", err)
		os.Exit(1)
	}

	if err := runInit(spec); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker init: %s\n", err)
		os.Exit(1)
	}
}

func runInit(spec initSpec) error {
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker init: warning: make mounts private: %s (continuing)\n", err)
	}

	if err := syscall.Mount("overlay", spec.MergedDir, "overlay", 0, spec.MountOpts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}

	// 保证 chroot 后要用到的挂载点存在
	if err := os.MkdirAll(filepath.Join(spec.MergedDir, "proc"), 0o755); err != nil {
		return fmt.Errorf("create /proc: %w", err)
	}

	if spec.NewNetns {
		if err := bringLoUp(); err != nil {
			return fmt.Errorf("bring up loopback: %w", err)
		}
	}

	if err := syscall.Chroot(spec.MergedDir); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	if err := os.Chdir(spec.WorkingDir); err != nil {
		return fmt.Errorf("chdir %q: %w", spec.WorkingDir, err)
	}

	// chroot 之后挂载，使容器看到属于自己 PID namespace 的 /proc
	if err := syscall.Mount("proc", "/proc", "proc", uintptr(syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC), ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}

	// 父进程通过 ExtraFiles[0]=FD3 通知网络已经配好,init 才可以 exec 用户进程
	waitForParent()

	return runAsPid1(spec)
}

// waitForParent 阻塞读 FD 3 一字节;FD 不存在(host 模式)就直接跳过
func waitForParent() {
	f := os.NewFile(3, "sync")
	if f == nil {
		return
	}
	defer f.Close()
	var b [1]byte
	_, _ = f.Read(b[:])
}

// runAsPid1 让 init 进程留在 PID 1 上，fork 出真正的用户命令，
// 自己负责信号转发和孤儿僵尸回收。正常路径不会返回（直接 os.Exit）。
func runAsPid1(spec initSpec) error {
	bin := lookupInRootfs(spec.Argv[0])

	childPid, err := syscall.ForkExec(bin, spec.Argv, &syscall.ProcAttr{
		Env:   spec.Env,
		Files: []uintptr{0, 1, 2},
	})
	if err != nil {
		return fmt.Errorf("exec %q: %w", bin, err)
	}

	sigCh := make(chan os.Signal, 16)
	signal.Notify(sigCh, forwardableSignals...)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if s, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(childPid, s)
			}
		}
	}()

	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return fmt.Errorf("wait4: %w", err)
		}
		if wpid == childPid {
			os.Exit(exitCodeFromStatus(ws))
		}
		// 其他孤儿子进程已经被本次 wait 回收，继续循环
	}
}

func exitCodeFromStatus(ws syscall.WaitStatus) int {
	switch {
	case ws.Exited():
		return ws.ExitStatus()
	case ws.Signaled():
		return 128 + int(ws.Signal())
	default:
		return -1
	}
}

var forwardableSignals = []os.Signal{
	syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM,
	syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGWINCH,
	syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU,
}

func lookupInRootfs(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/bin/" + name
}

// bringLoUp 通过 ioctl 启用回环接口，避免依赖 ip/ifconfig。
func bringLoUp() error {
	const (
		siocGifFlags = 0x8913
		siocSifFlags = 0x8914
		iffUp        = 0x1
	)
	type ifreqFlags struct {
		Name  [16]byte
		Flags uint16
		_pad  [22]byte
	}

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	var req ifreqFlags
	copy(req.Name[:], "lo")

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), siocGifFlags, uintptr(unsafe.Pointer(&req))); errno != 0 {
		return fmt.Errorf("SIOCGIFFLAGS: %w", errno)
	}
	req.Flags |= iffUp
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), siocSifFlags, uintptr(unsafe.Pointer(&req))); errno != 0 {
		return fmt.Errorf("SIOCSIFFLAGS: %w", errno)
	}
	return nil
}
