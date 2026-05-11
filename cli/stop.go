package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("stop", &StopCli{})
}

type StopCli struct {
}

func (*StopCli) Exec(args ...string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tinydocker stop <container-id>...")
	}
	for _, id := range args {
		if err := container.Stop(id); err != nil {
			return err
		}
	}
	return nil
}

func (*StopCli) Description() string {
	return "Stop one or more running containers"
}

func (*StopCli) UseRoot() bool {
	return false
}
