package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"tinydocker/config"
)

type networksState struct {
	Networks map[string]Network `json:"networks"`
}

// Create 创建一个 docker network
func Create(name, subnet, driver string) (*Network, error) {
	if name == "" {
		return nil, errors.New("network name cannot be empty")
	}
	if driver == "" {
		driver = DriverBridge
	}
	if driver != DriverBridge {
		return nil, fmt.Errorf("unsupported driver %q (only bridge)", driver)
	}
	gwIP, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("parse subnet: %w", err)
	}
	if gwIP.Equal(ipNet.IP) {
		gwIP = ipFromIndex(ipNet, 1)
	}
	if err := validateSubnet(ipNet); err != nil {
		return nil, err
	}

	nw := Network{
		Name:    name,
		Subnet:  ipNet.String(),
		Gateway: gwIP.String(),
		Driver:  driver,
	}

	if err := withNetworksLocked(func(s *networksState) error {
		if _, exists := s.Networks[name]; exists {
			return fmt.Errorf("network %q already exists", name)
		}
		// 1. 占住网关 IP(顺便初始化位图)
		if err := ReserveIP(ipNet, gwIP); err != nil {
			return fmt.Errorf("reserve gateway: %w", err)
		}
		// 2. 创建 bridge 接口 + 设 IP + up
		if err := createBridge(name, &net.IPNet{IP: gwIP, Mask: ipNet.Mask}); err != nil {
			_ = ReleaseIP(ipNet, gwIP)
			return err
		}
		// 3. 写 ip_forward
		if err := enableIPForward(); err != nil {
			fmt.Fprintf(os.Stderr, "warn: enable ip_forward: %s\n", err)
		}
		// 3b. 在桥上打开 route_localnet,让宿主机 127.0.0.1 → 映射端口的包
		// 经过 OUTPUT DNAT 后能正确路由到容器
		if err := enableRouteLocalnet(name); err != nil {
			fmt.Fprintf(os.Stderr, "warn: enable route_localnet on %s: %s\n", name, err)
		}
		// 4. iptables: MASQUERADE + FORWARD ACCEPT
		if _, err := addMasquerade(ipNet.String(), name); err != nil {
			_ = deleteBridge(name)
			_ = ReleaseIP(ipNet, gwIP)
			return err
		}
		if _, err := addLocalhostMasquerade(name); err != nil {
			// 不致命:只影响 127.0.0.1 → 容器映射端口,继续
			fmt.Fprintf(os.Stderr, "warn: add localhost MASQUERADE: %s\n", err)
		}
		if _, err := addForwardAccept(name); err != nil {
			// 不致命,继续
			fmt.Fprintf(os.Stderr, "warn: add FORWARD rules: %s\n", err)
		}
		s.Networks[name] = nw
		return nil
	}); err != nil {
		return nil, err
	}
	return &nw, nil
}

// List 返回所有 network,按名称排序
func List() ([]Network, error) {
	s, err := readNetworksState()
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(s.Networks))
	for _, n := range s.Networks {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get 按名称获取
func Get(name string) (*Network, error) {
	s, err := readNetworksState()
	if err != nil {
		return nil, err
	}
	if n, ok := s.Networks[name]; ok {
		return &n, nil
	}
	return nil, fmt.Errorf("network %q not found", name)
}

// Delete 拆除一个 network(撤 iptables + 删 bridge + 释放网关 + 删元数据)
func Delete(name string) error {
	return withNetworksLocked(func(s *networksState) error {
		nw, ok := s.Networks[name]
		if !ok {
			return fmt.Errorf("network %q not found", name)
		}
		ipNet, err := nw.SubnetIPNet()
		if err != nil {
			return err
		}

		// 倒着撤
		_ = iptablesDelete([]string{"-A", "FORWARD", "-i", name, "-j", "ACCEPT"})
		_ = iptablesDelete([]string{"-A", "FORWARD", "-o", name, "-j", "ACCEPT"})
		_ = iptablesDelete([]string{"-t", "nat", "-A", "POSTROUTING", "-m", "addrtype", "--src-type", "LOCAL", "-o", name, "-j", "MASQUERADE"})
		_ = iptablesDelete([]string{"-t", "nat", "-A", "POSTROUTING", "-s", ipNet.String(), "!", "-o", name, "-j", "MASQUERADE"})
		_ = deleteBridge(name)
		gw := net.ParseIP(nw.Gateway)
		if gw != nil {
			_ = ReleaseIP(ipNet, gw)
		}

		delete(s.Networks, name)
		return nil
	})
}

// EnsureDefault 保证默认 bridge 存在,返回它
func EnsureDefault() (*Network, error) {
	if nw, err := Get(DefaultBridgeName); err == nil {
		return nw, nil
	}
	subnet := config.C.Bip
	if subnet == "" {
		subnet = "172.17.0.1/16"
	}
	return Create(DefaultBridgeName, subnet, DriverBridge)
}

// ===== networks.json 持久化 =====

func readNetworksState() (*networksState, error) {
	if err := ensureLayout(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(networksFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return &networksState{Networks: map[string]Network{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s networksState
	if len(data) == 0 {
		return &networksState{Networks: map[string]Network{}}, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Networks == nil {
		s.Networks = map[string]Network{}
	}
	return &s, nil
}

func withNetworksLocked(fn func(*networksState) error) error {
	if err := ensureLayout(); err != nil {
		return err
	}
	return withLockedJSON(networksFilePath(), func(data []byte) ([]byte, error) {
		state := networksState{Networks: map[string]Network{}}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &state); err != nil {
				return nil, fmt.Errorf("decode networks: %w", err)
			}
			if state.Networks == nil {
				state.Networks = map[string]Network{}
			}
		}
		if err := fn(&state); err != nil {
			return nil, err
		}
		return json.MarshalIndent(&state, "", "  ")
	})
}
