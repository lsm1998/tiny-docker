package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"tinydocker/pkg/network"
)

func init() {
	registerCli("network", &NetworkCli{})
}

type NetworkCli struct{}

func (*NetworkCli) Description() string {
	return "Manage networks (create|ls|rm)"
}

func (*NetworkCli) Exec(args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinydocker network <create|ls|rm> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "create":
		return networkCreate(rest)
	case "ls", "list":
		return networkList(rest)
	case "rm", "remove":
		return networkRemove(rest)
	default:
		return fmt.Errorf("unknown subcommand %q (create|ls|rm)", sub)
	}
}

func networkCreate(args []string) error {
	subnet := ""
	driver := network.DriverBridge
	name := ""
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--subnet":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --subnet")
			}
			subnet = args[i]
		case "--driver":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --driver")
			}
			driver = args[i]
		default:
			if name != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			name = args[i]
		}
		i++
	}
	if name == "" {
		return fmt.Errorf("usage: tinydocker network create [--subnet CIDR] [--driver bridge] <name>")
	}
	if subnet == "" {
		return fmt.Errorf("--subnet is required")
	}
	nw, err := network.Create(name, subnet, driver)
	if err != nil {
		return err
	}
	fmt.Printf("created network %s (%s)\n", nw.Name, nw.Subnet)
	return nil
}

func networkList(_ []string) error {
	networks, err := network.List()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSUBNET\tGATEWAY\tDRIVER")
	for _, n := range networks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n.Name, n.Subnet, n.Gateway, n.Driver)
	}
	return w.Flush()
}

func networkRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinydocker network rm <name>")
	}
	for _, name := range args {
		if err := network.Delete(name); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", name)
	}
	return nil
}

func (*NetworkCli) UseRoot() bool {
	return true
}
