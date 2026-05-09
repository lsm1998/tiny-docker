package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tinydocker/pkg/image"
)

func Logs(id string) ([]byte, error) {
	path, err := LogsPath(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func LogsPath(id string) (string, error) {
	containersDir := filepath.Join(image.DataRoot(), "containers")

	entries, err := os.ReadDir(containersDir)
	if err != nil {
		return "", err
	}

	var matched string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), id) {
			if matched != "" {
				return "", fmt.Errorf("multiple containers match prefix %q", id)
			}
			matched = entry.Name()
		}
	}
	if matched == "" {
		return "", fmt.Errorf("container %q not found", id)
	}

	logPath := filepath.Join(containersDir, matched, "container.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return "", fmt.Errorf("no logs for container %q", id)
	}
	return logPath, nil
}
