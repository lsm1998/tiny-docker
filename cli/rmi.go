package cli

import (
	"fmt"

	"tinydocker/pkg/image"
)

func init() {
	registerCli("rmi", &cmdEntry{
		exec:        rmiExec,
		description: "Remove one or more images",
	})
}

func rmiExec(args ...string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tinydocker rmi <image>...")
	}
	for _, ref := range args {
		if err := image.RemoveImage(ref); err != nil {
			return err
		}
	}
	return nil
}