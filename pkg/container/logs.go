package container

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

// Logs 根据容器id读取日志全文。
func Logs(id string) ([]byte, error) {
	path, err := LogsPath(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// LogsPath 根据容器id获取日志路径。
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

// LogsWithFollow 把容器日志输出到 w，先输出已有内容，然后在指定时长内
// 轮询新数据持续输出。cancelled 收到信号时退出。用于实现 logs -f。
func LogsWithFollow(id string, w io.Writer, cancelled <-chan struct{}, pollInterval time.Duration) error {
	path, err := LogsPath(id)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 先输出已有内容
	if _, err := io.Copy(w, f); err != nil {
		return err
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cancelled:
			return nil
		case <-ticker.C:
			// 读新数据：Read 在文件尾返回 io.EOF 但不会阻塞，
			// 用 seek 确保偏移不被意外修改即可（上面的 Copy 已让我们在末尾）
			buf := make([]byte, 64*1024)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return werr
					}
				}
				if err != nil {
					break
				}
			}
		}
	}
}