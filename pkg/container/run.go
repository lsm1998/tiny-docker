package container

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"tinydocker/pkg/image"
)

// Run 启动容器
func Run(rawRef string, cmdArgs []string) error {
	img, err := image.FindImage(rawRef)
	if err != nil {
		return err
	}

	containerID, err := randomHex(32)
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
	defer os.RemoveAll(containerDir)

	lowerDirs := make([]string, len(img.Layers))
	for i, l := range img.Layers {
		lowerDirs[len(img.Layers)-1-i] = filepath.Join(image.OverlayRoot(), l.CacheID, "diff")
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		strings.Join(lowerDirs, ":"), upperDir, workDir)

	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	defer syscall.Unmount(mergedDir, syscall.MNT_DETACH)

	entrypointAndCmd := buildCommand(img.Entrypoint, img.Cmd, cmdArgs)
	if len(entrypointAndCmd) == 0 {
		return fmt.Errorf("image %q has no entrypoint or cmd", img.Name)
	}

	cmd := exec.Command(entrypointAndCmd[0], entrypointAndCmd[1:]...)
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		return fmt.Errorf("start container: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- cmd.Wait() }()

	select {
	case err := <-errCh:
		signal.Stop(sigCh)
		if err != nil {
			return fmt.Errorf("container: %w", err)
		}
		return nil
	case sig := <-sigCh:
		signal.Stop(sigCh)
		if err := cmd.Process.Signal(sig); err != nil {
			cmd.Process.Kill()
		}
		<-errCh
		return nil
	}
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

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
