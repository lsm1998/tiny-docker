package cli

import (
	"fmt"
	"strings"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("port", &cmdEntry{
		exec:        portExec,
		description: "List port mappings for a container",
	})
}

func portExec(args ...string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tinydocker port <container-id> [<port>]")
	}

	mappings, err := container.GetContainerPortMappings(args[0])
	if err != nil {
		return err
	}

	if len(args) >= 2 {
		target := args[1]
		if !strings.Contains(target, "/") {
			target = target + "/tcp"
		}
		for _, m := range mappings {
			portProto := fmt.Sprintf("%d/%s", m.ContainerPort, m.Protocol)
			if portProto == target {
				fmt.Printf("%s\n", formatPortMapping(m))
				return nil
			}
		}
		return fmt.Errorf("port %s is not mapped for container %q", args[1], args[0])
	}

	for _, m := range mappings {
		fmt.Printf("%d/%s -> %s\n", m.ContainerPort, m.Protocol, formatPortMapping(m))
	}
	return nil
}

func formatPortMapping(m container.PortMapping) string {
	if m.HostIP == "0.0.0.0" {
		return fmt.Sprintf("0.0.0.0:%d", m.HostPort)
	}
	return fmt.Sprintf("%s:%d", m.HostIP, m.HostPort)
}