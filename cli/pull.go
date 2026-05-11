package cli

import (
	"fmt"

	"tinydocker/pkg/image"
)

func init() {
	registerCli("pull", &cmdEntry{
		exec:        pullExec,
		description: "Pull an image",
	})
}

func pullExec(args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tinydocker pull <image>")
	}
	return image.Pull(args[0])
}