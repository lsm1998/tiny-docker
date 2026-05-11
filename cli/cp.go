package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("cp", &cmdEntry{
		exec:        cpExec,
		description: "Copy files/folders between a container and the local filesystem",
	})
}

func cpExec(args ...string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: tinydocker cp <container:src> <dst>  OR  tinydocker cp <src> <container:dst>")
	}

	src := args[0]
	dst := args[1]

	srcContainer, srcPath, srcIsContainer := parseContainerRef(src)
	dstContainer, dstPath, dstIsContainer := parseContainerRef(dst)

	switch {
	case dstIsContainer && !srcIsContainer:
		return copyToContainer(src, dstContainer, dstPath)
	case srcIsContainer && !dstIsContainer:
		return copyFromContainer(srcContainer, srcPath, dst)
	case srcIsContainer && dstIsContainer:
		return fmt.Errorf("copying between two containers is not supported")
	default:
		return fmt.Errorf("one of src or dst must be a container reference (container:path)")
	}
}

func parseContainerRef(s string) (containerName string, path string, isContainer bool) {
	isWindowsDrive := len(s) >= 2 && s[1] == ':' && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
	if isWindowsDrive {
		return "", "", false
	}
	con, p, err := container.ParseContainerPath(s)
	if err != nil {
		return "", "", false
	}
	return con, p, true
}

func copyToContainer(src string, containerName string, containerPath string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve src path: %w", err)
	}
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("stat src %s: %w", srcAbs, err)
	}
	return container.CopyToContainer(containerName, srcAbs, containerPath)
}

func copyFromContainer(containerName string, containerPath string, dst string) error {
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		dstAbs = dst
	}
	return container.CopyFromContainer(containerName, containerPath, dstAbs)
}