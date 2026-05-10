package network

import (
	"net"
	"os"
	"path/filepath"
	"tinydocker/pkg/image"
)

const (
	DefaultBridgeName = "tdbr0"
	DriverBridge      = "bridge"

	hostVethPrefix = "tdv-"
	peerVethPrefix = "tde-"
	containerEth   = "eth0"
)

// Network 描述一个 docker network 实例。
type Network struct {
	Name    string `json:"name"`
	Subnet  string `json:"subnet"`  // CIDR, e.g. 172.17.0.0/16
	Gateway string `json:"gateway"` // 网桥本身的 IP
	Driver  string `json:"driver"`
}

// SubnetIPNet 把 Subnet 字段解析回 *net.IPNet。
func (n *Network) SubnetIPNet() (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(n.Subnet)
	if err != nil {
		return nil, err
	}
	return ipNet, nil
}

// Endpoint 是容器与一个 network 的连接点。持久化以便清理。
type Endpoint struct {
	ContainerID  string     `json:"container_id"`
	NetworkName  string     `json:"network_name"`
	IP           string     `json:"ip"`
	HostVeth     string     `json:"host_veth"`
	PeerVeth     string     `json:"peer_veth"`
	IPTablesArgs [][]string `json:"iptables_args,omitempty"`
}

func dataRoot() string {
	return filepath.Join(image.DataRoot(), "network")
}

func networksFilePath() string {
	return filepath.Join(dataRoot(), "networks.json")
}

func ipamFilePath() string {
	return filepath.Join(dataRoot(), "ipam.json")
}

func endpointsDir() string {
	return filepath.Join(dataRoot(), "endpoints")
}

func endpointFile(id string) string {
	return filepath.Join(endpointsDir(), id+".json")
}

func ensureLayout() error {
	for _, d := range []string{dataRoot(), endpointsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
