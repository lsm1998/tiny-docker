package container

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
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

// PortForwarder 管理端口映射的 listener
type PortForwarder struct {
	listeners []net.Listener
	wg        sync.WaitGroup
}

// StartPortForwarders 启动所有端口映射的 listener
func StartPortForwarders(pid int, mappings []PortMapping, isHostNet bool) (*PortForwarder, error) {
	pf := &PortForwarder{}
	for _, m := range mappings {
		if m.Protocol != "tcp" {
			pf.Stop()
			return nil, fmt.Errorf("unsupported protocol %q (only tcp supported)", m.Protocol)
		}
		if isHostNet && m.HostPort == m.ContainerPort {
			continue // host 模式下相同端口无需转发
		}
		addr := fmt.Sprintf("%s:%d", m.HostIP, m.HostPort)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			pf.Stop()
			return nil, fmt.Errorf("listen %s: %w", addr, err)
		}
		pf.listeners = append(pf.listeners, ln)
		pf.wg.Add(1)
		go pf.serve(ln, pid, m, isHostNet)
	}
	return pf, nil
}

func (pf *PortForwarder) serve(ln net.Listener, pid int, m PortMapping, isHostNet bool) {
	defer pf.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go pf.handleConn(conn, pid, m, isHostNet)
	}
}

func (pf *PortForwarder) handleConn(conn net.Conn, pid int, m PortMapping, isHostNet bool) {
	defer conn.Close()

	var target io.ReadWriteCloser
	var err error

	if isHostNet {
		target, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", m.ContainerPort))
	} else {
		target, err = dialInContainerNetns(pid, fmt.Sprintf("127.0.0.1:%d", m.ContainerPort))
	}

	if err != nil {
		return
	}
	defer target.Close()

	done := make(chan struct{})
	var once sync.Once
	go func() {
		io.Copy(target, conn)
		once.Do(func() { close(done) })
	}()
	go func() {
		io.Copy(conn, target)
		once.Do(func() { close(done) })
	}()
	<-done
}

// Stop 停止所有端口映射 listener
func (pf *PortForwarder) Stop() {
	for _, ln := range pf.listeners {
		ln.Close()
	}
	pf.wg.Wait()
}

// dialInContainerNetns 进入容器的网络命名空间建立连接
func dialInContainerNetns(pid int, address string) (io.ReadWriteCloser, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}

	nsPath := fmt.Sprintf("/proc/%d/ns/net", pid)
	nsFile, err := os.Open(nsPath)
	if err != nil {
		return nil, fmt.Errorf("open container netns: %w", err)
	}
	defer nsFile.Close()

	origNs, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return nil, fmt.Errorf("open current netns: %w", err)
	}
	defer origNs.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := unix.Setns(int(nsFile.Fd()), unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("setns: %w", err)
	}
	defer func() {
		_ = unix.Setns(int(origNs.Fd()), unix.CLONE_NEWNET)
	}()

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("invalid IP: %s", host)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("IPv6 not supported")
	}

	sa := &syscall.SockaddrInet4{Port: port}
	copy(sa.Addr[:], ip4)

	if err := syscall.Connect(fd, sa); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect: %w", err)
	}

	return &netnsConn{fd: fd}, nil
}

type netnsConn struct {
	fd int
}

func (c *netnsConn) Read(b []byte) (int, error) {
	n, err := syscall.Read(c.fd, b)
	if n < 0 {
		n = 0
	}
	return n, err
}

func (c *netnsConn) Write(b []byte) (int, error) {
	n, err := syscall.Write(c.fd, b)
	if n < 0 {
		n = 0
	}
	return n, err
}

func (c *netnsConn) Close() error {
	return syscall.Close(c.fd)
}

// startPortmapDaemon 在 detach 模式下启动独立的端口映射守护进程
func startPortmapDaemon(containerDir, containerID string) error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	cmd := exec.Command(selfPath)
	cmd.Env = append(os.Environ(), "_TINYDOCKER_PORTMAP="+containerID)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	logFile, _ := os.OpenFile(filepath.Join(containerDir, "portmap.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start portmap daemon: %w", err)
	}

	pidFile := filepath.Join(containerDir, "portmap.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("write portmap pid: %w", err)
	}
	return nil
}

// RunPortmapDaemon 端口映射守护进程的主循环（由 _TINYDOCKER_PORTMAP 环境变量触发）
func RunPortmapDaemon(containerID string) error {
	dir, cfg, err := findContainerDir(containerID)
	if err != nil {
		return err
	}
	if cfg.Status != "running" {
		return fmt.Errorf("container %q is not running", containerID)
	}

	mappings, err := ParsePortMappings(cfg.PortMaps)
	if err != nil {
		return err
	}
	if len(mappings) == 0 {
		return nil
	}

	isHostNet := cfg.NetworkMode == "" || cfg.NetworkMode == NetworkHost
	pf, err := StartPortForwarders(cfg.Pid, mappings, isHostNet)
	if err != nil {
		return err
	}

	// 等待容器进程退出
	proc, _ := os.FindProcess(cfg.Pid)
	if proc != nil {
		proc.Wait()
	}

	pf.Stop()
	os.Remove(filepath.Join(dir, "portmap.pid"))
	return nil
}

// GetContainerPortMappings 获取容器的端口映射列表
func GetContainerPortMappings(id string) ([]PortMapping, error) {
	_, cfg, err := findContainerDir(id)
	if err != nil {
		return nil, err
	}
	return ParsePortMappings(cfg.PortMaps)
}

// stopPortmap 停止容器的端口映射守护进程
func stopPortmap(dir string) {
	pidFile := filepath.Join(dir, "portmap.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	os.Remove(pidFile)

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}

	proc, _ := os.FindProcess(pid)
	if proc != nil {
		proc.Signal(syscall.SIGTERM)
	}
}
