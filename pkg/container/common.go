package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tinydocker/pkg/image"

	"golang.org/x/sys/unix"
)

// FindContainerDir 查询容器配置
func FindContainerDir(ref string) (string, Config, error) {
	containersDir := filepath.Join(image.DataRoot(), "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		return "", Config{}, err
	}

	var (
		nameMatch   string
		nameCfg     Config
		prefixMatch string
		prefixCfg   Config
		prefixHits  int
	)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(containersDir, entry.Name())
		cfg, err := ReadConfig(dir)
		if err != nil {
			continue
		}
		if cfg.Name != "" && cfg.Name == ref {
			nameMatch = dir
			nameCfg = cfg
			break
		}
		if strings.HasPrefix(entry.Name(), ref) {
			prefixMatch = dir
			prefixCfg = cfg
			prefixHits++
		}
	}

	if nameMatch != "" {
		return nameMatch, nameCfg, nil
	}
	if prefixHits > 1 {
		return "", Config{}, fmt.Errorf("multiple containers match prefix %q", ref)
	}
	if prefixMatch == "" {
		return "", Config{}, fmt.Errorf("container %q not found", ref)
	}
	return prefixMatch, prefixCfg, nil
}

// validateName 检查 --name 参数的合法性。空字符串视为未指定，直接放行。
func validateName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 128 {
		return fmt.Errorf("container name %q is too long (max 128)", name)
	}
	first := name[0]
	if !isAlpha(first) && !isDigit(first) {
		return fmt.Errorf("container name must start with a letter or digit: %q", name)
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !isAlpha(c) && !isDigit(c) && c != '_' && c != '.' && c != '-' {
			return fmt.Errorf("container name contains invalid character %q", string(c))
		}
	}
	return nil
}

// ensureNameAvailable 拒绝与已有容器同名
func ensureNameAvailable(name string) error {
	containersDir := filepath.Join(image.DataRoot(), "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cfg, err := ReadConfig(filepath.Join(containersDir, entry.Name()))
		if err != nil {
			continue
		}
		if cfg.Name == name {
			return fmt.Errorf("container name %q is already in use by %s", name, cfg.ID)
		}
	}
	return nil
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func requireRoot(op string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("%s requires root (re-run with sudo)", op)
}

// isAlive 综合 status + Pid + /proc 判断容器是否还活着。比单看 cfg.Status 准一点。
func isAlive(cfg Config) bool {
	if cfg.Status != "running" || cfg.Pid <= 0 {
		return false
	}
	_, err := os.Stat(fmt.Sprintf("/proc/%d", cfg.Pid))
	return err == nil
}

// lockContainerDir 对容器目录加排他锁，保护并发读写 config.json。
func lockContainerDir(dir string) (*os.File, error) {
	lockPath := filepath.Join(dir, "config.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	return f, nil
}

// unlockContainerDir 释放容器目录锁。
func unlockContainerDir(f *os.File) {
	if f != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}
}

// withContainerLocked 持锁期间读写容器 config。
func withContainerLocked(dir string, fn func() error) error {
	lock, err := lockContainerDir(dir)
	if err != nil {
		return err
	}
	defer unlockContainerDir(lock)
	return fn()
}
