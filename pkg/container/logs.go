package container

import (
	"os"
	"path/filepath"
)

// Logs 根据容器id读取日志
func Logs(id string) ([]byte, error) {
	path, err := LogsPath(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// LogsPath 根据容器id获取日志路径
func LogsPath(id string) (string, error) {
	dir, _, err := FindContainerDir(id)
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(dir, "container.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return "", nil
	}
	return logPath, nil
}
