package cli

import (
	"fmt"

	"tinydocker/pkg/image"
)

func init() {
	registerCli("rmi", &RmiCli{})
}

type RmiCli struct {
}

func (*RmiCli) Exec(args ...string) error {
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

func (*RmiCli) Description() string {
	return "Remove one or more images"
}
