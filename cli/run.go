package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("run", &RunCli{})
}

type RunCli struct {
}

func (*RunCli) Exec(args ...string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tinydocker run <image> [command...]")
	}
	return container.Run(args[0], args[1:])
}

func (*RunCli) Description() string {
	return "Run a container"
}
