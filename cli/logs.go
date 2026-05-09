package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("logs", &LogsCli{})
}

type LogsCli struct {
}

func (*LogsCli) Exec(args ...string) error {
	follow := false
	i := 0
	for i < len(args) {
		if args[i] != "-f" {
			break
		}
		follow = true
		i++
	}
	if i >= len(args) {
		return fmt.Errorf("usage: tinydocker logs [-f] <container-id>")
	}

	logPath, err := container.LogsPath(args[i])
	if err != nil {
		return err
	}

	file, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(os.Stdout, file); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-sigCh:
			return nil
		case <-tick.C:
			if _, err := io.Copy(os.Stdout, file); err != nil {
				return err
			}
		}
	}
}

func (*LogsCli) Description() string {
	return "Fetch the logs of a container"
}
