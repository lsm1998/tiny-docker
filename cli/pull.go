package cli

import (
	"fmt"
	"tinydocker/pkg/image"
)

func init() {
	registerCli("pull", &PullCli{})
}

type PullCli struct {
}

func (*PullCli) Exec(args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tinydocker pull <image>")
	}
	return image.Pull(args[0])
}

func (*PullCli) Description() string {
	return "Pull an image"
}

func (*PullCli) UseRoot() bool {
	return false
}
