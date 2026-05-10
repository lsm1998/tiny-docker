package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"tinydocker/pkg/system"
)

const (
	MemoryMax   = "memory.max"     // 内存限制配置文件，用于设置cgroup的内存上限
	CpuMax      = "cpu.max"        // CPU限制配置文件，用于设置cgroup的CPU使用上限
	CgroupProcs = "cgroup.procs"   // cgroup进程列表文件，用于将进程加入指定的cgroup
	CgroupRoot  = "/sys/fs/cgroup" // cgroup挂载根目录，是Linux系统中管理控制组的默认路径
)

type CGroupManager struct {
	path string
}

var cgroupManagers = make([]CGroupManager, 0)

func NewCGroupManager(name string) *CGroupManager {
	// 拼接cgroup路径
	cgroupPath := filepath.Join(CgroupRoot, name)

	// 检查cgroup路径是否存在
	if _, err := os.Stat(cgroupPath); os.IsNotExist(err) {
		// 如果路径不存在，尝试创建路径
		if err := os.MkdirAll(cgroupPath, 0755); err != nil {
			system.Panic("Error creating cgroup: %v", err)
		}
	}

	// 返回CGroupManager对象
	cgroupManager := CGroupManager{path: cgroupPath}
	cgroupManagers = append(cgroupManagers, cgroupManager)
	return &cgroupManager
}

// Apply 将给定的进程ID（pid）加入到 cgroup 中
func (c *CGroupManager) Apply(pid int) error {
	// 将 pid 转换为字符串
	pidStr := strconv.Itoa(pid)

	// 将 pid 写入到 cgroup 的 procs 文件中
	if err := os.WriteFile(filepath.Join(c.path, CgroupProcs), []byte(pidStr), 0644); err != nil {
		// 如果写入失败，记录错误日志
		system.Error("Error applying pid to cgroup: %v", err)
		return err
	}

	// 写入成功，返回 nil
	return nil
}

// SetMemoryLimit 为CGroup设置内存限制
func (c *CGroupManager) SetMemoryLimit(memoryLimit string) error {
	// 拼接路径
	memPath := filepath.Join(c.path, MemoryMax)
	// 写入内存限制
	if err := os.WriteFile(memPath, []byte(memoryLimit), 0644); err != nil {
		// 记录错误日志
		system.Error("Error setting memory limit: %v", err)
		// 返回错误
		return err
	}
	// 禁用swap
	// 拼接swap路径
	swapPath := filepath.Join(c.path, "memory.swap.max")
	// 写入禁用swap配置
	if err := os.WriteFile(swapPath, []byte("0"), 0644); err != nil {
		// 记录错误日志
		system.Error("Failed to disable swap: %v", err)
		// 返回错误
		return err
	}

	return nil
}

// SetCPULimit 设置CPU限制
// 参数：
// cpusStr：表示CPU限制的字符串，格式为 "cpu_quota cpu_period"
// 返回值：
// error：如果设置失败，则返回错误信息；否则返回nil
func (c *CGroupManager) SetCPULimit(cpusStr string) error {
	// 设置CPU限制
	// 解析传入的CPU字符串
	cpuQuota, cpuPeriod, err := ParseCPUs(cpusStr)
	if err != nil {
		system.Error("Error parsing cpus: %v", err)
		return err
	}

	// 拼接CPU限制路径
	cpuMaxPath := filepath.Join(c.path, CpuMax)

	// 格式化CPU限制字符串
	cpuLimit := fmt.Sprintf("%d %d", cpuQuota, cpuPeriod)

	// 写入CPU限制到文件
	if err := os.WriteFile(cpuMaxPath, []byte(cpuLimit), 0644); err != nil {
		return err
	}

	return nil
}

// Cleanup 删除由c.path指定的目录及其所有子目录和文件。
// 如果删除过程中发生错误，会记录错误日志并返回错误。
// 如果没有错误发生，则返回nil。
func Cleanup() error {
	// 删除c.path指定的目录及其所有子目录和文件
	for _, c := range cgroupManagers {
		err := os.RemoveAll(c.path)
		if err != nil {
			// 记录错误日志
			system.Error("Error cleaning up cgroup: %v", err)
			// 返回错误
			continue
		}
	}

	return nil
}

// ParseCPUs 将 --cpus 字符串解析为 quota 和 period
// ParseCPUs 解析字符串形式的CPU值，并返回CPU配额（quota）和周期（period）
func ParseCPUs(cpusStr string) (int, int, error) {
	// 支持浮点数输入，例如 "1.5"
	cpusFloat, err := strconv.ParseFloat(cpusStr, 64)
	if err != nil {
		// 解析错误，返回错误信息
		return 0, 0, fmt.Errorf("invalid cpus value: %s", cpusStr)
	}

	if cpusFloat <= 0 {
		// 如果输入的CPU值小于等于0，返回错误信息
		return 0, 0, fmt.Errorf("cpus must be greater than 0")
	}

	// 固定周期为 100ms（CGroups 推荐值）
	const period = 100000 // microseconds
	// 计算quota值
	quota := int(cpusFloat * float64(period))

	// 防止溢出或非法值
	if quota <= 0 {
		// 如果计算出的quota值小于等于0，返回错误信息
		return 0, 0, fmt.Errorf("calculated quota is invalid: %d", quota)
	}

	return quota, period, nil
}
