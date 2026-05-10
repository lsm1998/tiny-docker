package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tinydocker/pkg/image"
)

// PortMapping 表示一个端口映射规则
type PortMapping struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

// ParsePortMappings 解析端口映射字符串列表
func ParsePortMappings(raws []string) ([]PortMapping, error) {
	mappings := make([]PortMapping, 0, len(raws))
	for _, raw := range raws {
		m, err := parsePortMapping(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid port mapping %q: %w", raw, err)
		}
		mappings = append(mappings, m)
	}
	return mappings, nil
}

func parsePortMapping(s string) (PortMapping, error) {
	protocol := "tcp"
	if i := strings.LastIndex(s, "/"); i >= 0 {
		protocol = s[i+1:]
		s = s[:i]
	}

	parts := strings.Split(s, ":")
	var hostIP, hostPortStr, containerPortStr string

	switch len(parts) {
	case 2:
		hostIP = "0.0.0.0"
		hostPortStr = parts[0]
		containerPortStr = parts[1]
	case 3:
		hostIP = parts[0]
		hostPortStr = parts[1]
		containerPortStr = parts[2]
	default:
		return PortMapping{}, fmt.Errorf("expected HOST:CONTAINER or IP:HOST:CONTAINER")
	}

	hostPort, err := strconv.Atoi(hostPortStr)
	if err != nil || hostPort < 1 || hostPort > 65535 {
		return PortMapping{}, fmt.Errorf("invalid host port %q", hostPortStr)
	}
	containerPort, err := strconv.Atoi(containerPortStr)
	if err != nil || containerPort < 1 || containerPort > 65535 {
		return PortMapping{}, fmt.Errorf("invalid container port %q", containerPortStr)
	}

	return PortMapping{
		HostIP:        hostIP,
		HostPort:      hostPort,
		ContainerPort: containerPort,
		Protocol:      protocol,
	}, nil
}

// GetContainerPortMappings 获取容器的端口映射列表
func GetContainerPortMappings(id string) ([]PortMapping, error) {
	_, cfg, err := findContainerDir(id)
	if err != nil {
		return nil, err
	}
	return ParsePortMappings(cfg.PortMaps)
}

// portsConflict 判断两条端口映射是否冲突
func portsConflict(a, b PortMapping) bool {
	if a.Protocol != b.Protocol || a.HostPort != b.HostPort {
		return false
	}
	if a.HostIP == b.HostIP {
		return true
	}
	return a.HostIP == "0.0.0.0" || b.HostIP == "0.0.0.0"
}

func validatePortMaps(newMaps []PortMapping) error {
	if len(newMaps) == 0 {
		return nil
	}
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
		if err != nil || !isAlive(cfg) {
			continue
		}
		existing, err := ParsePortMappings(cfg.PortMaps)
		if err != nil {
			continue
		}
		for _, e := range existing {
			for _, n := range newMaps {
				if portsConflict(e, n) {
					return fmt.Errorf("host port %s already in use by container %s",
						formatBinding(n), describeContainer(cfg))
				}
			}
		}
	}
	return nil
}

func formatBinding(m PortMapping) string {
	if m.HostIP == "0.0.0.0" || m.HostIP == "" {
		return fmt.Sprintf("%d/%s", m.HostPort, m.Protocol)
	}
	return fmt.Sprintf("%s:%d/%s", m.HostIP, m.HostPort, m.Protocol)
}

func describeContainer(cfg Config) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	if len(cfg.ID) > 12 {
		return cfg.ID[:12]
	}
	return cfg.ID
}
