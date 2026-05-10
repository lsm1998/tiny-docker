package cli

import (
	"encoding/json"
	"fmt"
	"tinydocker/pkg/container"
)

func init() {
	registerCli("inspect", &inspectCli{})
}

type inspectCli struct {
}

func (*inspectCli) Exec(args ...string) error {
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

func (*inspectCli) Description() string {
	return "Display detailed information on a container"
}
