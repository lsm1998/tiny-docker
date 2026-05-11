package cli

import (
	"encoding/json"
	"fmt"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("inspect", &cmdEntry{
		exec:        inspectExec,
		description: "Display detailed information on a container",
	})
}

func inspectExec(args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tinydocker inspect <container-id>")
	}
	_, cfg, err := container.FindContainerDir(args[0])
	if err != nil {
		return err
	}
	data, _ := json.Marshal(cfg)
	fmt.Println(string(data))
	return nil
}