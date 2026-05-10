package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tinydocker/pkg/image"
)

// findContainerDir 查询容器配置
func findContainerDir(id string) (string, Config, error) {
	containersDir := filepath.Join(image.DataRoot(), "containers")
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		return "", Config{}, err
	}

	var matched string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), id) {
			if matched != "" {
				return "", Config{}, fmt.Errorf("multiple containers match prefix %q", id)
			}
			matched = entry.Name()
		}
	}
	if matched == "" {
		return "", Config{}, fmt.Errorf("container %q not found", id)
	}

	dir := filepath.Join(containersDir, matched)
	cfg, err := ReadConfig(dir)
	if err != nil {
		return "", Config{}, err
	}
	return dir, cfg, nil
}
