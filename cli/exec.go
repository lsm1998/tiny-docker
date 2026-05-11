package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("exec", &cmdEntry{
		exec:        execExec,
		description: "Run a command in a running container",
	})
}

func execExec(args ...string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: tinydocker exec <container> <command> [args...]")
	}
	return container.Exec(args[0], args[1:])
}