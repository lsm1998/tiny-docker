package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const cgroupParent = "tinydocker"

func EnsureParent() error {
	parent := filepath.Join(CgroupRoot, cgroupParent)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("mkdir cgroup parent: %w", err)
	}
	sub := filepath.Join(parent, "cgroup.subtree_control")
	if err := os.WriteFile(sub, []byte("+cpu +memory +pids"), 0o644); err != nil {
		return fmt.Errorf("enable controllers on %s: %w", sub, err)
	}
	return nil
}

// LeafName 给定 containerID 返回叶子 cgroup 的相对名(传给 NewCGroupManager)
func LeafName(containerID string) string {
	return filepath.Join(cgroupParent, containerID)
}

// LeafPath 返回叶子 cgroup 完整路径,供外部清理使用
func LeafPath(containerID string) string {
	return filepath.Join(CgroupRoot, cgroupParent, containerID)
}

// RemoveLeaf 删除叶子 cgroup
func RemoveLeaf(containerID string) error {
	if err := os.Remove(LeafPath(containerID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// ParseMemoryLimit 解析内存限制
func ParseMemoryLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty memory limit")
	}
	if s == "max" {
		return -1, nil
	}
	// 允许 KB/MB/GB 这种两字符后缀
	mult := int64(1)
	num := s
	switch {
	case strings.HasSuffix(strings.ToLower(s), "kb"), strings.HasSuffix(strings.ToLower(s), "k"):
		mult = 1 << 10
	case strings.HasSuffix(strings.ToLower(s), "mb"), strings.HasSuffix(strings.ToLower(s), "m"):
		mult = 1 << 20
	case strings.HasSuffix(strings.ToLower(s), "gb"), strings.HasSuffix(strings.ToLower(s), "g"):
		mult = 1 << 30
	case strings.HasSuffix(strings.ToLower(s), "b"):
		mult = 1
	}
	if mult > 1 || strings.HasSuffix(strings.ToLower(s), "b") {
		// 去掉后缀(可能是 1 或 2 字符)
		switch {
		case strings.HasSuffix(strings.ToLower(s), "kb"),
			strings.HasSuffix(strings.ToLower(s), "mb"),
			strings.HasSuffix(strings.ToLower(s), "gb"):
			num = s[:len(s)-2]
		default:
			num = s[:len(s)-1]
		}
	}
	v, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid memory value %q", s)
	}
	return v * mult, nil
}
