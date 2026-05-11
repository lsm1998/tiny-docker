package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"tinydocker/pkg/image"
	"tinydocker/pkg/stringutil"
	"tinydocker/pkg/timeutil"
)

func init() {
	registerCli("images", &cmdEntry{
		exec:        imagesExec,
		description: "List local images",
	})
}

func imagesExec(args ...string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: tinydocker images")
	}

	images, err := image.List()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REPOSITORY\tTAG\tIMAGE ID\tCREATED\tSIZE")
	for _, item := range images {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\n",
			item.Repository,
			item.Tag,
			stringutil.ShortImageID(item.ID),
			timeutil.FormatRelativeTime(item.CreatedAt),
			stringutil.FormatBytes(item.SizeBytes),
		)
	}
	return tw.Flush()
}