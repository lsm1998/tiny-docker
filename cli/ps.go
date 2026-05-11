package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"tinydocker/pkg/container"
	"tinydocker/pkg/stringutil"
	"tinydocker/pkg/timeutil"
)

func init() {
	registerCli("ps", &PsCli{})
}

type PsCli struct {
}

func (*PsCli) Exec(args ...string) error {
	all := false
	for _, arg := range args {
		switch arg {
		case "-a", "--all":
			all = true
		default:
			return fmt.Errorf("unknown flag: %s", arg)
		}
	}

	containers, err := container.List(all)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER ID\tIMAGE\tCOMMAND\tCREATED\tSTATUS\tNAMES")

	if len(containers) == 0 {
		return tw.Flush()
	}

	for _, item := range containers {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			stringutil.ShortImageID(item.ID),
			item.Image,
			item.Command,
			timeutil.FormatRelativeTime(item.CreatedAt),
			formatStatus(item.Status, item.ExitCode),
			item.Name,
		)
	}
	return tw.Flush()
}

func (*PsCli) Description() string {
	return "List containers"
}

func formatStatus(status string, exitCode int) string {
	if status == "running" {
		return "Up"
	}
	return fmt.Sprintf("Exited (%d)", exitCode)
}

func (*PsCli) UseRoot() bool {
	return false
}
