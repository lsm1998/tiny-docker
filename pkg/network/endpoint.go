package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// PortBinding 是一个待加 DNAT 的端口映射
type PortBinding struct {
	HostIP        string
	HostPort      int
	ContainerPort int
	Protocol      string
}

// ConnectContainer 把容器接入指定 network:
//  1. 分配 IP
//  2. 创建 veth、推 peer 进容器 netns、配置 IP/默认路由
//  3. 为每个端口映射添加 PREROUTING/OUTPUT DNAT 规则
//  4. 落盘 endpoint
//
// 任何一步失败都会回滚之前已经做完的步骤,避免泄漏。
func ConnectContainer(networkName, containerID string, pid int, ports []PortBinding) (*Endpoint, error) {
	if networkName == "" {
		networkName = DefaultBridgeName
	}
	nw, err := EnsureNetwork(networkName)
	if err != nil {
		return nil, err
	}
	// 复用旧 bridge 时也要保证 sysctl 是对的,这两个都是幂等写
	if err := enableIPForward(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: enable ip_forward: %s\n", err)
	}
	if err := enableRouteLocalnet(nw.Name); err != nil {
		fmt.Fprintf(os.Stderr, "warn: enable route_localnet on %s: %s\n", nw.Name, err)
	}
	// 老 network 在升级前可能没加 LOCAL→bridge MASQUERADE,这里幂等补上
	if _, err := addLocalhostMasquerade(nw.Name); err != nil {
		fmt.Fprintf(os.Stderr, "warn: ensure localhost MASQUERADE on %s: %s\n", nw.Name, err)
	}
	subnet, err := nw.SubnetIPNet()
	if err != nil {
		return nil, err
	}

	ip, err := AllocateIP(subnet)
	if err != nil {
		return nil, fmt.Errorf("allocate ip: %w", err)
	}
	rollback := []func(){
		func() { _ = ReleaseIP(subnet, ip) },
	}
	doRollback := func() {
		for i := len(rollback) - 1; i >= 0; i-- {
			rollback[i]()
		}
	}

	hostVeth, peerVeth, err := connectVeth(nw.Name, containerID, pid, nw.Gateway, ip.String(), subnet)
	if err != nil {
		doRollback()
		return nil, err
	}
	rollback = append(rollback, func() { _ = disconnectVeth(hostVeth) })

	ep := &Endpoint{
		ContainerID: containerID,
		NetworkName: nw.Name,
		IP:          ip.String(),
		HostVeth:    hostVeth,
		PeerVeth:    peerVeth,
	}

	for _, p := range ports {
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		if p.Protocol != "tcp" {
			doRollback()
			return nil, fmt.Errorf("unsupported protocol %q (only tcp)", p.Protocol)
		}
		rules, err := addDNAT(p.HostIP, p.HostPort, ip.String(), p.ContainerPort, p.Protocol)
		if err != nil {
			doRollback()
			return nil, err
		}
		for _, r := range rules {
			ep.IPTablesArgs = append(ep.IPTablesArgs, r)
		}
	}

	if err := saveEndpoint(ep); err != nil {
		// 反向撤掉 iptables
		for _, r := range ep.IPTablesArgs {
			_ = iptablesDelete(r)
		}
		doRollback()
		return nil, err
	}
	return ep, nil
}

func ReleaseEndpoint(containerID string) error {
	ep, err := loadEndpoint(containerID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var firstErr error
	noteErr := func(e error) {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}

	for _, r := range ep.IPTablesArgs {
		if e := iptablesDelete(r); e != nil {
			fmt.Fprintf(os.Stderr, "warn: iptables delete: %s\n", e)
			noteErr(e)
		}
	}
	if ep.HostVeth != "" {
		if e := disconnectVeth(ep.HostVeth); e != nil {
			fmt.Fprintf(os.Stderr, "warn: delete veth %s: %s\n", ep.HostVeth, e)
			noteErr(e)
		}
	}
	// 上面任何一步失败,先保留 endpoint 文件,等有权限的进程再清理,避免 ghost 规则
	if firstErr != nil {
		return firstErr
	}

	if ep.NetworkName != "" && ep.IP != "" {
		if nw, e := Get(ep.NetworkName); e == nil {
			if subnet, se := nw.SubnetIPNet(); se == nil {
				if ip := net.ParseIP(ep.IP); ip != nil {
					_ = ReleaseIP(subnet, ip)
				}
			}
		}
	}
	return os.Remove(endpointFile(containerID))
}

// EnsureNetwork 取已存在的 network,默认 bridge 不存在时自动创建
func EnsureNetwork(name string) (*Network, error) {
	if name == DefaultBridgeName {
		return EnsureDefault()
	}
	return Get(name)
}

func saveEndpoint(ep *Endpoint) error {
	if err := ensureLayout(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(endpointFile(ep.ContainerID), data, 0o644)
}

func loadEndpoint(containerID string) (*Endpoint, error) {
	data, err := os.ReadFile(endpointFile(containerID))
	if err != nil {
		return nil, err
	}
	var ep Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// GarbageCollect 扫描 endpoints 目录,清理已死容器的残留
// isAlive 由调用方提供:接收容器 ID,返回是否还活着。出错或不确定时返回 true(更安全)
func GarbageCollect(isAlive func(containerID string) bool) {
	entries, err := os.ReadDir(endpointsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		id := name[:len(name)-len(".json")]
		if isAlive != nil && isAlive(id) {
			continue
		}
		if err := ReleaseEndpoint(id); err != nil {
			fmt.Fprintf(os.Stderr, "warn: gc endpoint %s: %s\n", id, err)
		}
	}
}
