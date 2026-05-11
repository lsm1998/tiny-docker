package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("exec", &ExecCli{})
}

type ExecCli struct{}

func (*ExecCli) Description() string {
	return "Run a command in a running container"
}

func (*ExecCli) Exec(args ...string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: tinydocker exec <container> <command> [args...]")
	}
	return container.Exec(args[0], args[1:])
}

func (*ExecCli) UseRoot() bool {
	return false
}
