package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// 这里包装 iptables 命令的添加 / 删除。
// 添加规则后会把原始参数返回,持久化到 endpoint;删除时把 -A/-I 改成 -D 重跑即可。

// iptablesAdd 执行 iptables 命令并返回原参数(供清理时反向执行)
func iptablesAdd(args []string) ([]string, error) {
	cmd := exec.Command("iptables", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
	}
	saved := append([]string(nil), args...)
	return saved, nil
}

// iptablesEnsure 幂等:把 -A/-I 改成 -C 检查,已存在则跳过,否则照常追加
func iptablesEnsure(args []string) ([]string, error) {
	checkArgs := append([]string(nil), args...)
	for i, a := range checkArgs {
		switch a {
		case "-A", "-I":
			checkArgs[i] = "-C"
		case "--append", "--insert":
			checkArgs[i] = "--check"
		}
	}
	if err := exec.Command("iptables", checkArgs...).Run(); err == nil {
		return append([]string(nil), args...), nil
	}
	return iptablesAdd(args)
}

// iptablesDelete 把保存的 -A/-I 参数转成 -D 再跑一次。容忍 "rule does not exist"。
func iptablesDelete(args []string) error {
	if len(args) == 0 {
		return nil
	}
	dArgs := append([]string(nil), args...)
	for i, a := range dArgs {
		switch a {
		case "-A", "-I":
			dArgs[i] = "-D"
		case "--append", "--insert":
			dArgs[i] = "--delete"
		}
	}
	cmd := exec.Command("iptables", dArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "does not exist" 视为成功
		if strings.Contains(string(out), "does a matching rule exist") ||
			strings.Contains(string(out), "No chain/target/match by that name") ||
			strings.Contains(string(out), "Bad rule") {
			return nil
		}
		return fmt.Errorf("iptables %s: %s", strings.Join(dArgs, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// addMasquerade 为某个网段开启出网 NAT
func addMasquerade(subnet, bridge string) ([]string, error) {
	return iptablesAdd([]string{
		"-t", "nat",
		"-A", "POSTROUTING",
		"-s", subnet,
		"!", "-o", bridge,
		"-j", "MASQUERADE",
	})
}

// addLocalhostMasquerade 把宿主本地源(包括 127.0.0.1) → bridge 的流量做 SNAT。
// 不加这条,从宿主访问 127.0.0.1:HOST_PORT 时,OUTPUT 链 DNAT 后包带着 src=127.0.0.1
// 从 tdbr0 出去,容器 netns 在 eth0 收到 127.0.0.0/8 源会按 martian 丢弃。
// 幂等:已加过则跳过,这样老 network(没有这条规则)在新容器接入时也能补上。
func addLocalhostMasquerade(bridge string) ([]string, error) {
	return iptablesEnsure([]string{
		"-t", "nat",
		"-A", "POSTROUTING",
		"-m", "addrtype", "--src-type", "LOCAL",
		"-o", bridge,
		"-j", "MASQUERADE",
	})
}

// addForwardAccept 允许 bridge 进出方向的转发(应对 FORWARD 默认 DROP 的发行版)
func addForwardAccept(bridge string) ([][]string, error) {
	rules := [][]string{
		{"-A", "FORWARD", "-o", bridge, "-j", "ACCEPT"},
		{"-A", "FORWARD", "-i", bridge, "-j", "ACCEPT"},
	}
	saved := make([][]string, 0, len(rules))
	for _, r := range rules {
		s, err := iptablesAdd(r)
		if err != nil {
			// 出错时回滚已加的部分
			for _, prev := range saved {
				_ = iptablesDelete(prev)
			}
			return nil, err
		}
		saved = append(saved, s)
	}
	return saved, nil
}

// addDNAT 为单个端口映射添加 PREROUTING 与 OUTPUT 两条 DNAT
func addDNAT(hostIP string, hostPort int, containerIP string, containerPort int, proto string) ([][]string, error) {
	if hostIP == "" || hostIP == "0.0.0.0" {
		hostIP = ""
	}
	target := fmt.Sprintf("%s:%d", containerIP, containerPort)

	build := func(chain string) []string {
		args := []string{
			"-t", "nat",
			"-A", chain,
			"-p", proto,
			"-m", proto,
		}
		if hostIP != "" {
			args = append(args, "-d", hostIP)
		}
		args = append(args,
			"--dport", fmt.Sprintf("%d", hostPort),
			"-j", "DNAT",
			"--to-destination", target,
		)
		return args
	}

	saved := make([][]string, 0, 2)
	for _, chain := range []string{"PREROUTING", "OUTPUT"} {
		s, err := iptablesAdd(build(chain))
		if err != nil {
			for _, prev := range saved {
				_ = iptablesDelete(prev)
			}
			return nil, err
		}
		saved = append(saved, s)
	}
	return saved, nil
}

// enableIPForward 写 /proc/sys/net/ipv4/ip_forward = 1
func enableIPForward() error {
	const path = "/proc/sys/net/ipv4/ip_forward"
	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) == "1" {
		return nil
	}
	return os.WriteFile(path, []byte("1\n"), 0o644)
}

// enableRouteLocalnet 在指定桥上打开 route_localnet,允许 127.0.0.0/8 源 IP 的包
// 经由该桥转发,这样宿主机访问 127.0.0.1:<host_port> 经过 OUTPUT DNAT 后
// 还能成功路由到容器
func enableRouteLocalnet(iface string) error {
	path := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/route_localnet", iface)
	return os.WriteFile(path, []byte("1\n"), 0o644)
}
