package container

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tinydocker/pkg/image"
	"tinydocker/pkg/network"
)

type Config struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	ImageName   string   `json:"image_name"`
	Command     string   `json:"command"`
	CreatedAt   string   `json:"created_at"`
	Status      string   `json:"status"`
	ExitCode    int      `json:"exit_code"`
	Pid         int      `json:"pid"`
	PortMaps    []string `json:"port_maps,omitempty"`
	NetworkMode string   `json:"network_mode,omitempty"`
	NetworkName string   `json:"network_name,omitempty"`
	IPAddress   string   `json:"ip_address,omitempty"`
}

type Options struct {
	Detach      bool     // 是否后台运行
	Rm          bool     // 退出后自动清理 containerDir
	PortMaps    []string // 端口映射
	Name        string   // 容器名称
	NetworkMode string   // 网络模式 bridge|host|none,空表示自动(root→bridge,非 root→host)
	NetworkName string   // bridge 模式下使用的网络名,空表示默认 tdbr0
}

const (
	NetworkBridge = "bridge"
	NetworkHost   = "host"
	NetworkNone   = "none"
)

// Run 启动容器
func Run(rawRef string, cmdArgs []string, opts Options) error {
	if err := validateName(opts.Name); err != nil {
		return err
	}
	if opts.Name != "" {
		if err := ensureNameAvailable(opts.Name); err != nil {
			return err
		}
	}

	if opts.Detach && opts.Rm {
		return fmt.Errorf("--rm cannot be used with -d (detached)")
	}

	mappings, err := ParsePortMappings(opts.PortMaps)
	if err != nil {
		return err
	}

	uid := os.Getuid()
	gid := os.Getgid()

	// 解析网络模式:用户没指定时,root 走 bridge,非 root 回退 host(否则 netlink/iptables 没权限)
	switch opts.NetworkMode {
	case "":
		if uid == 0 {
			opts.NetworkMode = NetworkBridge
		} else {
			opts.NetworkMode = NetworkHost
			if len(mappings) == 0 {
				fmt.Fprintln(os.Stderr, "warning: running as non-root, falling back to host network (bridge requires CAP_NET_ADMIN)")
			}
		}
	case NetworkBridge, NetworkHost, NetworkNone:
	default:
		return fmt.Errorf("unsupported network mode %q (bridge|host|none)", opts.NetworkMode)
	}
	if opts.NetworkMode == NetworkBridge && uid != 0 {
		return fmt.Errorf("network mode %q requires root or CAP_NET_ADMIN", NetworkBridge)
	}
	if len(mappings) > 0 && opts.NetworkMode != NetworkBridge {
		return fmt.Errorf("port mapping (-p) is only supported in bridge network mode")
	}
	if err := validatePortMaps(mappings); err != nil {
		return err
	}
	newNetns := opts.NetworkMode == NetworkBridge || opts.NetworkMode == NetworkNone

	img, err := image.FindImage(rawRef)
	if err != nil {
		return err
	}

	containerID, err := randomHex(16)
	if err != nil {
		return fmt.Errorf("generate container id: %w", err)
	}

	containerDir := filepath.Join(image.DataRoot(), "containers", containerID)
	upperDir := filepath.Join(containerDir, "upper")
	workDir := filepath.Join(containerDir, "work")
	mergedDir := filepath.Join(containerDir, "merged")

	for _, dir := range []string{upperDir, workDir, mergedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create container dirs: %w", err)
		}
	}

	lowerDirs := make([]string, len(img.Layers))
	for i, l := range img.Layers {
		lowerDirs[len(img.Layers)-1-i] = filepath.Join(image.OverlayRoot(), l.CacheID, "diff")
	}
	mountOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		strings.Join(lowerDirs, ":"), upperDir, workDir)

	entrypointAndCmd := buildCommand(img.Entrypoint, img.Cmd, cmdArgs)
	if len(entrypointAndCmd) == 0 {
		os.RemoveAll(containerDir)
		return fmt.Errorf("image %q has no entrypoint or cmd", img.Name)
	}

	workingDir := img.WorkingDir
	if workingDir == "" {
		workingDir = "/"
	}

	specJSON, err := json.Marshal(initSpec{
		MergedDir:  mergedDir,
		MountOpts:  mountOpts,
		WorkingDir: workingDir,
		Env:        img.Env,
		Argv:       entrypointAndCmd,
		NewNetns:   newNetns,
	})
	if err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("marshal init spec: %w", err)
	}

	cfg := Config{
		ID:          containerID,
		Name:        opts.Name,
		ImageName:   img.Name,
		Command:     strings.Join(entrypointAndCmd, " "),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Status:      "running",
		PortMaps:    opts.PortMaps,
		NetworkMode: opts.NetworkMode,
		NetworkName: opts.NetworkName,
	}
	if err := writeConfig(containerDir, cfg); err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("write container config: %w", err)
	}

	selfPath, err := os.Executable()
	if err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("locate self executable: %w", err)
	}

	cmd := exec.Command(selfPath)
	cmd.Env = append(os.Environ(), initEnvKey+"="+string(specJSON))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cloneflags := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS)
	if newNetns {
		cloneflags |= syscall.CLONE_NEWNET
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneflags,
	}

	// 非 root 用户需要创建 user namespace 做 UID 映射(只可能用于 host/none 模式)
	if uid != 0 {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: uid, Size: 1},
		}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: gid, Size: 1},
		}
	}

	// bridge 模式建立同步管道,让 init 在 ForkExec 用户进程前等父进程把 veth/iptables 配好
	var syncWriter *os.File
	if opts.NetworkMode == NetworkBridge {
		r, w, err := os.Pipe()
		if err != nil {
			os.RemoveAll(containerDir)
			return fmt.Errorf("create sync pipe: %w", err)
		}
		cmd.ExtraFiles = []*os.File{r}
		syncWriter = w
		// 父进程不再持有 read 端,Start 之后立刻关闭
		defer func() {
			if syncWriter != nil {
				syncWriter.Close()
			}
		}()
	}

	if opts.Detach {
		cmd.SysProcAttr.Setpgid = true
		cmd.Stdin = nil
		logPath := filepath.Join(containerDir, "container.log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		cfg.Status = "exited"
		cfg.ExitCode = -1
		writeConfig(containerDir, cfg)
		return fmt.Errorf("start container: %w", err)
	}
	// fork 后,父进程持有的 read 端可以关闭(child 已继承)
	if cmd.ExtraFiles != nil {
		for _, f := range cmd.ExtraFiles {
			f.Close()
		}
		cmd.ExtraFiles = nil
	}

	cfg.Pid = cmd.Process.Pid
	writeConfig(containerDir, cfg)

	// bridge 模式:配置容器 netns 网络 + iptables DNAT
	if opts.NetworkMode == NetworkBridge {
		bindings := make([]network.PortBinding, 0, len(mappings))
		for _, m := range mappings {
			bindings = append(bindings, network.PortBinding{
				HostIP:        m.HostIP,
				HostPort:      m.HostPort,
				ContainerPort: m.ContainerPort,
				Protocol:      m.Protocol,
			})
		}
		ep, err := network.ConnectContainer(opts.NetworkName, containerID, cfg.Pid, bindings)
		if err != nil {
			cmd.Process.Kill()
			cfg.Status = "exited"
			cfg.ExitCode = -1
			writeConfig(containerDir, cfg)
			return fmt.Errorf("connect network: %w", err)
		}
		cfg.NetworkName = ep.NetworkName
		cfg.IPAddress = ep.IP
		writeConfig(containerDir, cfg)

		// 通知 init 网络已就绪
		if syncWriter != nil {
			syncWriter.Write([]byte("1"))
			syncWriter.Close()
			syncWriter = nil
		}
	}

	if opts.Detach {
		fmt.Printf("%s\n", containerID)
		return nil
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- cmd.Wait() }()

	finalize := func(err error) error {
		cfg.Status = "exited"
		cfg.ExitCode = exitCode(err)
		writeConfig(containerDir, cfg)
		if opts.NetworkMode == NetworkBridge {
			if e := network.ReleaseEndpoint(containerID); e != nil {
				fmt.Fprintf(os.Stderr, "warn: release endpoint: %s\n", e)
			}
		}
		if opts.Rm {
			syscall.Unmount(mergedDir, syscall.MNT_DETACH)
			os.RemoveAll(containerDir)
		}
		return err
	}

	select {
	case err := <-errCh:
		signal.Stop(sigCh)
		if err := finalize(err); err != nil {
			return fmt.Errorf("container: %w", err)
		}
		return nil
	case sig := <-sigCh:
		signal.Stop(sigCh)
		if err := cmd.Process.Signal(sig); err != nil {
			cmd.Process.Kill()
		}
		err := <-errCh
		_ = finalize(err)
		return nil
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// buildCommand 构建命令
func buildCommand(entrypoint, cmd, args []string) []string {
	if len(args) > 0 {
		if len(entrypoint) > 0 {
			return append(append([]string{}, entrypoint...), args...)
		}
		return append([]string{}, args...)
	}
	if len(entrypoint) > 0 {
		result := append([]string{}, entrypoint...)
		if len(cmd) > 0 {
			result = append(result, cmd...)
		}
		return result
	}
	return append([]string{}, cmd...)
}

// resolveExecutable 解析可执行文件
func resolveExecutable(rootfs, name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		if _, err := os.Stat(filepath.Join(rootfs, dir, name)); err == nil {
			return dir + "/" + name
		}
	}
	return "/bin/" + name
}

func configPath(containerDir string) string {
	return filepath.Join(containerDir, "config.json")
}

func writeConfig(containerDir string, cfg Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(containerDir), data, 0o644)
}

// ReadConfig reads a container's config.json from its directory.
func ReadConfig(containerDir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(containerDir, "config.json"))
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
