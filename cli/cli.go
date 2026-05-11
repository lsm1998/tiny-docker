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

// cmdEntry 实现 DockerCli，通过字段注入 Exec 和 Description 行为。
type cmdEntry struct {
	exec        func(args ...string) error
	description string
	useRoot     bool
}

func (e *cmdEntry) Exec(args ...string) error { return e.exec(args...) }

func (e *cmdEntry) Description() string { return e.description }

func (e *cmdEntry) UseRoot() bool { return e.useRoot }

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

func registerCli(cmd string, entry *cmdEntry) {
	cliMap[cmd] = entry
}