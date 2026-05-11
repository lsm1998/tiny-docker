package cli

import (
	"fmt"
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

	if !follow {
		data, err := container.Logs(args[i])
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	cancelled := make(chan struct{})
	go func() {
		<-sigCh
		close(cancelled)
	}()

	return container.LogsWithFollow(args[i], os.Stdout, cancelled, 200*time.Millisecond)
}

func (*LogsCli) Description() string {
	return "Fetch the logs of a container"
}
func (*LogsCli) UseRoot() bool {
	return false
}
