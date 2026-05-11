package cli

import (
	"fmt"

	"tinydocker/pkg/system"
)

var cliMap = map[string]DockerCli{}

type DockerCli interface {
	Exec(args ...string) error
	Description() string
	UseRoot() bool
}

type cliManager struct {
}

func NewCliManager() *cliManager {
	return &cliManager{}
}

func (*cliManager) Run(args []string) error {
	cmd := args[0]
	cli, ok := cliMap[cmd]
	if !ok {
		return fmt.Errorf("command [%s] not found", cmd)
	}
	if cli.UseRoot() {
		if err := system.RequireRoot(cmd); err != nil {
			return err
		}
	}
	return cli.Exec(args[1:]...)
}

func registerCli(cmd string, cli DockerCli) {
	cliMap[cmd] = cli
}