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
	ImageName string   `json:"image_name"`
	Command   string   `json:"command"`
	CreatedAt string   `json:"created_at"`
	Status    string   `json:"status"`
	ExitCode  int      `json:"exit_code"`
	Pid       int      `json:"pid"`
	PortMaps  []string `json:"port_maps,omitempty"`
}

type Options struct {
	Detach   bool     // 是否后台运行
	PortMaps []string // 端口映射
}

// Run 启动容器
func Run(rawRef string, cmdArgs []string, opts Options) error {
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

	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, mountOpts); err != nil {
		os.RemoveAll(containerDir)
		return fmt.Errorf("mount overlay: %w", err)
	}
	defer syscall.Unmount(mergedDir, syscall.MNT_DETACH)

	entrypointAndCmd := buildCommand(img.Entrypoint, img.Cmd, cmdArgs)
	if len(entrypointAndCmd) == 0 {
		os.RemoveAll(containerDir)
		return fmt.Errorf("image %q has no entrypoint or cmd", img.Name)
	}

	bin := resolveExecutable(mergedDir, entrypointAndCmd[0])

	cfg := Config{
		ID:        containerID,
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

	cmd := exec.Command(bin, entrypointAndCmd[1:]...)
	cmd.Dir = img.WorkingDir
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
	cmd.Env = img.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		Chroot: mergedDir,
	}

	if opts.Detach {
		cmd.SysProcAttr.Setpgid = true
		cmd.Stdin = nil
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
