package cli

import (
	"fmt"
	"strings"

	"tinydocker/pkg/container"
)

func init() {
	registerCli("run", &RunCli{})
}

type RunCli struct {
}

func (*RunCli) Exec(args ...string) error {
	opts := container.Options{}
	i := 0
	for i < len(args) {
		if !strings.HasPrefix(args[i], "-") {
			break
		}
		switch args[i] {
		case "-d", "--detach":
			opts.Detach = true
		case "--rm":
			opts.Rm = true
		case "-p", "--publish":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing port mapping for -p")
			}
			opts.PortMaps = append(opts.PortMaps, args[i])
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing port mapping for --name")
			}
			opts.Name = args[i]
		case "--network":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --network")
			}
			switch args[i] {
			case container.NetworkHost, container.NetworkNone:
				opts.NetworkMode = args[i]
			default:
				opts.NetworkMode = container.NetworkBridge
				opts.NetworkName = args[i]
			}
		case "-m", "--memory":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --memory")
			}
			opts.Memory = args[i]
		case "--cpus":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --cpus")
			}
			opts.CPUs = args[i]
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
		i++
	}
	if i >= len(args) {
		return fmt.Errorf("usage: tinydocker run [OPTIONS] IMAGE [COMMAND...]")
	}
	return container.Run(args[i], args[i+1:], opts)
}

func (*RunCli) Description() string {
	return "Run a container"
}
