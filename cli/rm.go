package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("rm", &RmCli{})
}

type RmCli struct {
}

func (*RmCli) Exec(args ...string) error {
	force := false
	i := 0
	for i < len(args) {
		if args[i] != "-f" {
			break
		}
		force = true
		i++
	}
	if i >= len(args) {
		return fmt.Errorf("usage: tinydocker rm [-f] <container-id>...")
	}
	for _, id := range args[i:] {
		if err := container.Remove(id, force); err != nil {
			return err
		}
	}
	return nil
}

func (*RmCli) Description() string {
	return "Remove one or more containers"
}

func (*RmCli) UseRoot() bool {
	return false
}
