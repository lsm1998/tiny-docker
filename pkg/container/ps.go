package container

import (
	"os"
	"path/filepath"
	"sort"

	"tinydocker/pkg/image"
)

// Info 容器信息
type Info struct {
	ID        string
	Name      string
	Image     string
	Command   string
	CreatedAt string
	Status    string
	ExitCode  int
}

// List 返回容器信息
func List(all bool) ([]Info, error) {
	containersDir := filepath.Join(image.DataRoot(), "containers")

	entries, err := os.ReadDir(containersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	containers := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cfg, err := ReadConfig(filepath.Join(containersDir, entry.Name()))
		if err != nil {
			continue
		}
		if !all && cfg.Status != "running" {
			continue
		}
		containers = append(containers, Info{
			ID:        cfg.ID,
			Name:      cfg.Name,
			Image:     cfg.ImageName,
			Command:   cfg.Command,
			CreatedAt: cfg.CreatedAt,
			Status:    cfg.Status,
			ExitCode:  cfg.ExitCode,
		})
	}

	sort.Slice(containers, func(i, j int) bool {
		return containers[i].CreatedAt > containers[j].CreatedAt
	})
	return containers, nil
}
