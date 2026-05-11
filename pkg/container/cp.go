package container

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func joinPath(containerDir, containerPath string) string {
	return filepath.Join(containerDir, "merged", containerPath)
}

// CopyToContainer 把宿主机 src 复制到容器的 dst 路径中。
func CopyToContainer(containerRef, src, dst string) error {
	dir, _, err := FindContainerDir(containerRef)
	if err != nil {
		return err
	}
	merged := filepath.Join(dir, "merged")
	if _, err := os.Stat(merged); err != nil {
		return fmt.Errorf("container filesystem not available at %s: %w", merged, err)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat src: %w", err)
	}

	target := filepath.Join(merged, dst)

	if srcInfo.IsDir() {
		targetBase := filepath.Base(src)
		if !strings.HasSuffix(dst, "/") {
			target = filepath.Join(merged, dst, targetBase)
		}
		if err := os.MkdirAll(target, srcInfo.Mode()); err != nil {
			return fmt.Errorf("mkdir target: %w", err)
		}
		return copyDir(src, target)
	}

	// 单文件: 如果 dst 是目录或以 / 结尾，放进目录里
	if strings.HasSuffix(dst, "/") {
		if err := os.MkdirAll(filepath.Join(merged, dst), 0o755); err != nil {
			return fmt.Errorf("mkdir target: %w", err)
		}
		target = filepath.Join(merged, dst, filepath.Base(src))
	} else if dstInfo, err := os.Stat(target); err == nil && dstInfo.IsDir() {
		target = filepath.Join(target, filepath.Base(src))
	} else {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir target dir: %w", err)
		}
	}

	return copyFile(src, target, srcInfo.Mode())
}

// CopyFromContainer 把容器的 src 路径复制到宿主机 dst。
func CopyFromContainer(containerRef, src, dst string) error {
	dir, _, err := FindContainerDir(containerRef)
	if err != nil {
		return err
	}
	merged := filepath.Join(dir, "merged")
	if _, err := os.Stat(merged); err != nil {
		return fmt.Errorf("container filesystem not available at %s: %w", merged, err)
	}

	containerSrc := filepath.Join(merged, src)
	srcInfo, err := os.Stat(containerSrc)
	if err != nil {
		return fmt.Errorf("stat container path %s: %w", src, err)
	}

	if srcInfo.IsDir() {
		targetBase := filepath.Base(src)
		if !strings.HasSuffix(dst, "/") {
			dst = filepath.Join(dst, targetBase)
		}
		if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
			return fmt.Errorf("mkdir target: %w", err)
		}
		return copyDir(containerSrc, dst)
	}

	if strings.HasSuffix(dst, "/") || dst == "." {
		if dst == "." {
			var err error
			dst, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("mkdir target: %w", err)
		}
		dst = filepath.Join(dst, filepath.Base(src))
	} else if dstInfo, err := os.Stat(dst); err == nil && dstInfo.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	} else {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir target dir: %w", err)
		}
	}

	return copyFile(containerSrc, dst, srcInfo.Mode())
}

// ParseContainerPath 解析 "container:path" 或 "container:/path" 格式。
// 返回 containerRef 和 path。
func ParseContainerPath(s string) (containerRef string, path string, err error) {
	ref, rest, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", fmt.Errorf("expected container:path format, got %q", s)
	}
	path = rest
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return ref, path, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode())
		case info.Mode()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		default:
			return copyFile(path, target, info.Mode())
		}
	})
}