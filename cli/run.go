package cli

import (
	"fmt"
)

func init() {
	registerCli("run", &RunCli{})
}

type RunCli struct {
}

func (*RunCli) Exec(args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tinydocker run <image>")
	}
	return nil
}

func (*RunCli) Description() string {
	return "Run a container"
}
