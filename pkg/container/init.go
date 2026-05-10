package container

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const initEnvKey = "_TINYDOCKER_INIT"

type initSpec struct {
	MergedDir  string
	MountOpts  string
	WorkingDir string
	Env        []string
	Argv       []string
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
		return fmt.Errorf("make mounts private: %w", err)
	}

	if err := syscall.Mount("overlay", spec.MergedDir, "overlay", 0, spec.MountOpts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}

	if err := syscall.Chroot(spec.MergedDir); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	if err := os.Chdir(spec.WorkingDir); err != nil {
		return fmt.Errorf("chdir %q: %w", spec.WorkingDir, err)
	}

	bin := lookupInRootfs(spec.Argv[0])
	return syscall.Exec(bin, spec.Argv, spec.Env)
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
