package container

import (
	"fmt"
	"strconv"
	"strings"
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
