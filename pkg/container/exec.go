package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	execPidEnv = "_TINYDOCKER_EXEC_PID"
)

func Exec(ref string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command specified")
	}
	_, cfg, err := FindContainerDir(ref)
	if err != nil {
		return err
	}
	if cfg.Status != "running" || cfg.Pid <= 0 {
		return fmt.Errorf("container %q is not running", ref)
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", cfg.Pid)); err != nil {
		return fmt.Errorf("target pid %d no longer exists", cfg.Pid)
	}

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self executable: %w", err)
	}

	cmd := exec.Command(selfPath, argv...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", execPidEnv, cfg.Pid))
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
	pidStr := os.Getenv(execPidEnv)
	if pidStr == "" {
		return
	}
	os.Unsetenv(execPidEnv)

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker exec: invalid pid %q\n", pidStr)
		os.Exit(1)
	}
	if err := runExecContainer(pid, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "tinydocker exec: %s\n", err)
		os.Exit(1)
	}
	os.Exit(1)
}

func runExecContainer(targetPid int, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command")
	}
	type nsRef struct {
		name string
		flag int
	}
	specs := []nsRef{
		{"ipc", unix.CLONE_NEWIPC},
		{"uts", unix.CLONE_NEWUTS},
		{"net", unix.CLONE_NEWNET},
		{"pid", unix.CLONE_NEWPID},
		{"mnt", unix.CLONE_NEWNS},
	}
	files := make([]*os.File, 0, len(specs))
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, s := range specs {
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/%s", targetPid, s.name))
		if err != nil {
			return fmt.Errorf("open %s ns: %w", s.name, err)
		}
		files = append(files, f)
	}

	runtime.LockOSThread()
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare CLONE_FS: %w", err)
	}
	for i, s := range specs {
		if err := unix.Setns(int(files[i].Fd()), s.flag); err != nil {
			return fmt.Errorf("setns %s: %w", s.name, err)
		}
	}

	_ = os.Chdir("/")

	bin := lookupContainerBin(argv[0])

	return syscall.Exec(bin, argv, os.Environ())
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
