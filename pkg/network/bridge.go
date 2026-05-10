package network

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/vishvananda/netlink"
)

// createBridge 创建 bridge,设置 IP/up
func createBridge(name string, gw *net.IPNet) error {
	if _, err := netlink.LinkByName(name); err == nil {
		return fmt.Errorf("interface %q already exists", name)
	}
	la := netlink.NewLinkAttrs()
	la.Name = name
	br := &netlink.Bridge{LinkAttrs: la}
	if err := netlink.LinkAdd(br); err != nil {
		return fmt.Errorf("add bridge %s: %w", name, err)
	}
	addr, err := netlink.ParseAddr(gw.String())
	if err != nil {
		_ = netlink.LinkDel(br)
		return fmt.Errorf("parse bridge addr: %w", err)
	}
	if err := netlink.AddrAdd(br, addr); err != nil {
		_ = netlink.LinkDel(br)
		return fmt.Errorf("set bridge ip: %w", err)
	}
	if err := netlink.LinkSetUp(br); err != nil {
		_ = netlink.LinkDel(br)
		return fmt.Errorf("link up bridge: %w", err)
	}
	return nil
}

func deleteBridge(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return netlink.LinkDel(link)
}

// connectVeth 创建 veth 对、host 端挂到 bridge、peer 推入容器 netns、在容器 netns 内配 IP
func connectVeth(bridgeName string, containerID string, pid int, gw, containerIP string, subnet *net.IPNet) (hostName, peerName string, err error) {
	id5 := containerID
	if len(id5) > 5 {
		id5 = id5[:5]
	}
	hostName = hostVethPrefix + id5
	peerName = peerVethPrefix + id5

	br, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return "", "", fmt.Errorf("bridge %s not found: %w", bridgeName, err)
	}

	la := netlink.NewLinkAttrs()
	la.Name = hostName
	la.MasterIndex = br.Attrs().Index
	veth := &netlink.Veth{
		LinkAttrs: la,
		PeerName:  peerName,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return "", "", fmt.Errorf("add veth: %w", err)
	}

	cleanup := func() {
		if l, e := netlink.LinkByName(hostName); e == nil {
			_ = netlink.LinkDel(l)
		}
	}

	if err := netlink.LinkSetUp(veth); err != nil {
		cleanup()
		return "", "", fmt.Errorf("set host veth up: %w", err)
	}

	peer, err := netlink.LinkByName(peerName)
	if err != nil {
		cleanup()
		return "", "", fmt.Errorf("find peer veth: %w", err)
	}
	nsFile, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		cleanup()
		return "", "", fmt.Errorf("open container netns: %w", err)
	}
	defer nsFile.Close()
	if err := netlink.LinkSetNsFd(peer, int(nsFile.Fd())); err != nil {
		cleanup()
		return "", "", fmt.Errorf("move peer to netns: %w", err)
	}

	// 进容器 netns 配置 eth0 + 默认路由
	if err := withContainerNetns(pid, func() error {
		return configureContainerSide(peerName, containerEth, containerIP, gw, subnet.Mask)
	}); err != nil {
		cleanup()
		return "", "", err
	}

	return hostName, peerName, nil
}

func configureContainerSide(peerName, finalName, ip, gw string, mask net.IPMask) error {
	link, err := netlink.LinkByName(peerName)
	if err != nil {
		return fmt.Errorf("peer not in container netns: %w", err)
	}
	if err := netlink.LinkSetName(link, finalName); err != nil {
		return fmt.Errorf("rename to %s: %w", finalName, err)
	}
	link, err = netlink.LinkByName(finalName)
	if err != nil {
		return err
	}

	cidr := &net.IPNet{IP: net.ParseIP(ip), Mask: mask}
	addr, err := netlink.ParseAddr(cidr.String())
	if err != nil {
		return fmt.Errorf("parse container ip: %w", err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("add ip: %w", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("link up: %w", err)
	}
	// lo 由 init 进程拉起,这里不动

	gwIP := net.ParseIP(gw)
	if gwIP == nil {
		return fmt.Errorf("invalid gateway %s", gw)
	}
	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Gw:        gwIP,
		Dst:       nil, // default route
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("add default route: %w", err)
	}
	return nil
}

// disconnectVeth 删除 host 端 veth(peer 端会随容器 netns 销毁)
func disconnectVeth(hostName string) error {
	link, err := netlink.LinkByName(hostName)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return netlink.LinkDel(link)
}
