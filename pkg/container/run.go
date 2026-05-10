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
)

type Config struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	ImageName string   `json:"image_name"`
	Command   string   `json:"command"`
	CreatedAt string   `json:"created_at"`
	Status    string   `json:"status"`
	ExitCode  int      `json:"exit_code"`
	Pid       int      `json:"pid"`
	PortMaps  []string `json:"port_maps,omitempty"`
}

type Options struct {
	Detach      bool     // 是否后台运行
	Rm          bool     // 退出后自动清理 containerDir
	PortMaps    []string // 端口映射
	Name        string   // 容器名称
	NetworkMode string   // 网络模式 默认host
}

const (
	NetworkHost = "host"
	NetworkNone = "none"
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

	switch opts.NetworkMode {
	case "":
		opts.NetworkMode = NetworkHost
	case NetworkHost, NetworkNone:
	default:
		return fmt.Errorf("unsupported network mode %q (host|none)", opts.NetworkMode)
	}
	isolatedNet := opts.NetworkMode == NetworkNone

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

	uid := os.Getuid()
	gid := os.Getgid()

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
		MergedDir:   mergedDir,
		MountOpts:   mountOpts,
		WorkingDir:  workingDir,
		Env:         img.Env,
		Argv:        entrypointAndCmd,
		IsolatedNet: isolatedNet,
	})
	if err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("marshal init spec: %w", err)
	}

	cfg := Config{
		ID:        containerID,
		Name:      opts.Name,
		ImageName: img.Name,
		Command:   strings.Join(entrypointAndCmd, " "),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Status:    "running",
		PortMaps:  opts.PortMaps,
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

	cloneflags := uintptr(syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS |
		syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS)
	if isolatedNet {
		cloneflags |= syscall.CLONE_NEWNET
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneflags,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: uid, Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: gid, Size: 1},
		},
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

	cfg.Pid = cmd.Process.Pid
	writeConfig(containerDir, cfg)

	if opts.Detach {
		fmt.Printf("%s\n", containerID)
		return nil
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- cmd.Wait() }()

	select {
	case err := <-errCh:
		signal.Stop(sigCh)
		cfg.Status = "exited"
		cfg.ExitCode = exitCode(err)
		writeConfig(containerDir, cfg)
		if err != nil {
			return fmt.Errorf("container: %w", err)
		}
		return nil
	case sig := <-sigCh:
		signal.Stop(sigCh)
		if err := cmd.Process.Signal(sig); err != nil {
			cmd.Process.Kill()
		}
		err := <-errCh
		cfg.Status = "exited"
		cfg.ExitCode = exitCode(err)
		writeConfig(containerDir, cfg)
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
