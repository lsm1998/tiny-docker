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
	"strconv"
	"strings"
	"syscall"
	"time"

	"tinydocker/cgroups"
	"tinydocker/config"
	"tinydocker/pkg/image"
	"tinydocker/pkg/network"
	"tinydocker/pkg/system"
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
	Envs        []string // 环境变量
	Volumes     []string // 目录挂载
	NetworkMode string   // 网络模式 bridge|host|none
	NetworkName string   // bridge 模式下使用的网络名,空表示默认 tdbr0
	Memory      string   // --memory,如 "512m" / "1g" / "1048576",空表示不限
	CPUs        string   // --cpus,如 "1.5",空表示不限
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
	if opts.NetworkMode == NetworkBridge {
		if err := system.RequireRoot("bridge network mode"); err != nil {
			return err
		}
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

	resolvedEnv := mergeContainerEnv(img.Env, opts.Envs)

	resolvedVolumes, err := parseVolumes(opts.Volumes)
	if err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("parse volumes: %w", err)
	}

	specJSON, err := json.Marshal(initSpec{
		MergedDir:  mergedDir,
		MountOpts:  mountOpts,
		WorkingDir: workingDir,
		Env:        resolvedEnv,
		Argv:       entrypointAndCmd,
		NewNetns:   newNetns,
		DNS:        config.C.Dns,
		Volumes:    resolvedVolumes,
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

	if uid != 0 {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: uid, Size: 1},
		}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: gid, Size: 1},
		}
	}

	var syncWriter *os.File
	if opts.NetworkMode == NetworkBridge {
		r, w, err := os.Pipe()
		if err != nil {
			os.RemoveAll(containerDir)
			return fmt.Errorf("create sync pipe: %w", err)
		}
		cmd.ExtraFiles = []*os.File{r}
		syncWriter = w
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

	// 应用 cgroup 资源限制(--memory / --cpus)。任何一步失败都 kill + 清 cgroup。
	if err := applyCgroup(containerID, opts, cfg.Pid); err != nil {
		cmd.Process.Kill()
		_ = cgroups.RemoveLeaf(containerID)
		cfg.Status = "exited"
		cfg.ExitCode = -1
		writeConfig(containerDir, cfg)
		return fmt.Errorf("apply cgroup: %w", err)
	}

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
			_ = cgroups.RemoveLeaf(containerID)
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
		if e := cgroups.RemoveLeaf(containerID); e != nil {
			fmt.Fprintf(os.Stderr, "warn: remove cgroup: %s\n", e)
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
	return system.WriteFileAtomic(configPath(containerDir), data, 0o644)
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

// applyCgroup 在 /sys/fs/cgroup/tinydocker/<id>/ 创建 leaf,按 opts 设 limit,
// 把容器 init pid 加进去。没传 --memory 也没传 --cpus 时啥也不做。
func applyCgroup(containerID string, opts Options, pid int) error {
	if opts.Memory == "" && opts.CPUs == "" {
		return nil
	}
	if err := cgroups.EnsureParent(); err != nil {
		return err
	}
	cm := cgroups.NewCGroupManager(cgroups.LeafName(containerID))
	if opts.Memory != "" {
		bytes, err := cgroups.ParseMemoryLimit(opts.Memory)
		if err != nil {
			return fmt.Errorf("--memory: %w", err)
		}
		if err := cm.SetMemoryLimit(strconv.FormatInt(bytes, 10)); err != nil {
			return fmt.Errorf("set memory: %w", err)
		}
	}
	if opts.CPUs != "" {
		if err := cm.SetCPULimit(opts.CPUs); err != nil {
			return fmt.Errorf("set cpus: %w", err)
		}
	}
	if err := cm.Apply(pid); err != nil {
		return fmt.Errorf("attach pid: %w", err)
	}
	return nil
}

// mergeContainerEnv 合并镜像环境变量与用户通过 -e 传入的变量
func mergeContainerEnv(imageEnv, userEnv []string) []string {
	merged := mergeEnvLists(imageEnv, userEnv)
	if !hasEnvKey(merged, "PATH") {
		merged = append(merged, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	return merged
}

func mergeEnvLists(groups ...[]string) []string {
	merged := make([]string, 0)
	indexByKey := make(map[string]int)
	for _, group := range groups {
		for _, value := range group {
			key := envKey(value)
			if index, ok := indexByKey[key]; ok {
				merged[index] = value
				continue
			}
			indexByKey[key] = len(merged)
			merged = append(merged, value)
		}
	}
	return merged
}

func envKey(value string) string {
	if key, _, ok := strings.Cut(value, "="); ok {
		return key
	}
	return value
}

func hasEnvKey(envs []string, key string) bool {
	prefix := key + "="
	for _, value := range envs {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// parseVolumes 解析 -v 传入的 volume 字符串列表，返回 volumeSpec 切片。
// 格式: host_path:container_path[:ro]
func parseVolumes(raws []string) ([]volumeSpec, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	result := make([]volumeSpec, 0, len(raws))
	for _, raw := range raws {
		v, err := parseVolume(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func parseVolume(raw string) (volumeSpec, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return volumeSpec{}, fmt.Errorf("invalid volume spec %q (expected host_path:container_path[:ro])", raw)
	}

	source := parts[0]
	target := filepath.Clean(parts[1])
	if !strings.HasPrefix(target, "/") {
		return volumeSpec{}, fmt.Errorf("volume target %q must be an absolute path", target)
	}

	readOnly := false
	if len(parts) == 3 {
		if parts[2] == "ro" {
			readOnly = true
		} else {
			return volumeSpec{}, fmt.Errorf("unsupported volume option %q (only 'ro' is supported)", parts[2])
		}
	}

	// 检查源目录存在，且是目录
	info, err := os.Stat(source)
	if err != nil {
		return volumeSpec{}, fmt.Errorf("volume source %q: %w", source, err)
	}
	if !info.IsDir() {
		return volumeSpec{}, fmt.Errorf("volume source %q must be a directory", source)
	}

	// 转绝对路径，避免 chroot 后找不到
	absSource, err := filepath.Abs(source)
	if err != nil {
		return volumeSpec{}, fmt.Errorf("resolve absolute path for %q: %w", source, err)
	}

	return volumeSpec{
		Source:   absSource,
		Target:   target,
		ReadOnly: readOnly,
	}, nil
}
