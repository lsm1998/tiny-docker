package cli

import (
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("stop", &cmdEntry{
		exec:        stopExec,
		description: "Stop one or more running containers",
	})
}

func stopExec(args ...string) error {
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