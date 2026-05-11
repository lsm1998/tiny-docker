package container

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"tinydocker/cgroups"

	"golang.org/x/sys/unix"
)

const (
	execSpecEnv = "_TINYDOCKER_EXEC_SPEC"
)

type execSpec struct {
	TargetPid   int      `json:"target_pid"`
	MergedDir   string   `json:"merged_dir"`
	ContainerID string   `json:"container_id"`
	Argv        []string `json:"argv"`
	Env         []string `json:"env"`
}

func Exec(ref string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command specified")
	}
	dir, cfg, err := FindContainerDir(ref)
	if err != nil {
		return err
	}
	if cfg.Status != "running" || cfg.Pid <= 0 {
		return fmt.Errorf("container %q is not running", ref)
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", cfg.Pid)); err != nil {
		return fmt.Errorf("target pid %d no longer exists", cfg.Pid)
	}

	mergedDir := filepath.Join(dir, "merged")
	if _, err := os.Stat(mergedDir); err != nil {
		return fmt.Errorf("container merged dir not found: %w", err)
	}

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self executable: %w", err)
	}

	spec := execSpec{
		TargetPid:   cfg.Pid,
		MergedDir:   mergedDir,
		ContainerID: cfg.ID,
		Argv:        argv,
		Env:         os.Environ(),
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal exec spec: %w", err)
	}

	cmd := exec.Command(selfPath)
	cmd.Env = append(os.Environ(), execSpecEnv+"="+string(specJSON))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func MaybeRunExecContainer() {
	raw := os.Getenv(execSpecEnv)
	if raw == "" {
		return
	}
	os.Unsetenv(execSpecEnv)

	var spec execSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker exec: parse spec: %s\n", err)
		os.Exit(1)
	}
	if err := runExecContainer(&spec); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker exec: %s\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runExecContainer(spec *execSpec) error {
	if len(spec.Argv) == 0 {
		return fmt.Errorf("no command")
	}

	type nsSpec struct {
		name string
		flag int
	}
	nsList := []nsSpec{
		{"ipc", unix.CLONE_NEWIPC},
		{"uts", unix.CLONE_NEWUTS},
		{"net", unix.CLONE_NEWNET},
		{"pid", unix.CLONE_NEWPID},
		{"mnt", unix.CLONE_NEWNS},
	}

	nsFiles := make([]*os.File, 0, len(nsList))
	defer func() {
		for _, f := range nsFiles {
			f.Close()
		}
	}()
	for _, ns := range nsList {
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/%s", spec.TargetPid, ns.name))
		if err != nil {
			return fmt.Errorf("open %s ns: %w", ns.name, err)
		}
		nsFiles = append(nsFiles, f)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare CLONE_FS: %w", err)
	}

	// 按顺序加入各 namespace。mnt 必须在 pid 之前加入，因为加入 pid ns 后
	// fork 的子进程需要继承已经加入的 mnt namespace。
	for i, ns := range nsList {
		if ns.name == "pid" {
			// pid namespace 留到最后通过 clone 处理
			continue
		}
		if err := unix.Setns(int(nsFiles[i].Fd()), ns.flag); err != nil {
			return fmt.Errorf("setns %s: %w", ns.name, err)
		}
	}

	// 加入 PID namespace：在当前线程已经加入了其他所有 namespace 后，
	// fork 一个子进程并让子进程加入目标的 PID namespace。
	// 子进程在目标 PID namespace 中看到容器完整的进程树。
	exitCh := make(chan int, 1)

	// fork 前需要取得 pid ns fd（第 4 个）
	pidNsFd := int(nsFiles[3].Fd())

	pid, err := forkChild(pidNsFd, spec.ContainerID)
	if err != nil {
		return fmt.Errorf("fork: %w", err)
	}

	if pid != 0 {
		// 父进程等子进程退出
		go func() {
			var ws unix.WaitStatus
			_, err := unix.Wait4(pid, &ws, 0, nil)
			if err != nil {
				exitCh <- 1
				return
			}
			if ws.Exited() {
				exitCh <- ws.ExitStatus()
			} else if ws.Signaled() {
				exitCh <- 128 + int(ws.Signal())
			} else {
				exitCh <- 1
			}
		}()
		os.Exit(<-exitCh)
	}

	// 子进程：chroot 到容器 rootfs，然后 exec 用户命令
	if err := syscall.Chroot(spec.MergedDir); err != nil {
		fmt.Fprintf(os.Stderr, "chroot %s: %v\n", spec.MergedDir, err)
		os.Exit(1)
	}
	if err := os.Chdir("/"); err != nil {
		fmt.Fprintf(os.Stderr, "chdir /: %v\n", err)
		os.Exit(1)
	}

	bin := lookupContainerBin(spec.Argv[0])
	if err := syscall.Exec(bin, spec.Argv, spec.Env); err != nil {
		fmt.Fprintf(os.Stderr, "exec %s: %v\n", bin, err)
		os.Exit(1)
	}
	return nil
}

// forkChild 使用 clone 系统调用创建一个子进程，让子进程加入目标 PID namespace
func forkChild(targetPidNsFd int, containerID string) (int, error) {
	flags := uintptr(unix.SIGCHLD)

	pid1, _, errno := unix.RawSyscall(unix.SYS_CLONE, flags, 0, 0)
	if errno != 0 {
		return 0, fmt.Errorf("clone (first fork): %w", errno)
	}

	if pid1 != 0 {
		// 祖父进程：返回第一个子进程的 PID
		return int(pid1), nil
	}

	// 第一个子进程：加入目标 PID namespace
	if err := unix.Setns(targetPidNsFd, unix.CLONE_NEWPID); err != nil {
		fmt.Fprintf(os.Stderr, "setns pid: %v\n", err)
		os.Exit(1)
	}

	// 再 fork：孙进程会在目标的 PID namespace 中
	pid2, _, errno := unix.RawSyscall(unix.SYS_CLONE, flags, 0, 0)
	if errno != 0 {
		fmt.Fprintf(os.Stderr, "clone (second fork): %v\n", errno)
		os.Exit(1)
	}

	if pid2 != 0 {
		// 第一个子进程 wait 孙进程，转发 exit code
		var ws unix.WaitStatus
		_, err := unix.Wait4(int(pid2), &ws, 0, nil)
		if err != nil {
			os.Exit(1)
		}
		if ws.Exited() {
			os.Exit(ws.ExitStatus())
		}
		if ws.Signaled() {
			// 模拟 shell 的 128+signal 约定
			os.Exit(128 + int(ws.Signal()))
		}
		os.Exit(1)
	}

	if containerID != "" {
		leafName := cgroups.LeafName(containerID)
		cm := cgroups.NewCGroupManager(leafName)
		_ = cm.Apply(os.Getpid())
	}

	return 0, nil
}

func lookupContainerBin(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		for _, dir := range filepath.SplitList(pathEnv) {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return name
}
