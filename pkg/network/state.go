package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tinydocker/pkg/system"

	"golang.org/x/sys/unix"
)

// withLockedJSON 串行化对 path 的 read-modify-write
func withLockedJSON(path string, fn func(data []byte) ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lf.Close()
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock %s: %w", lockPath, err)
	}
	defer unix.Flock(int(lf.Fd()), unix.LOCK_UN)

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := fn(data)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return system.WriteFileAtomic(path, out, 0o644)
}
