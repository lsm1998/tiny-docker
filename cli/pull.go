package cli

const registry = "registry-1.docker.io"

func init() {
	registerCli("pull", &PullCli{})
}

type PullCli struct {
}

func (*PullCli) Exec(args ...string) error {
	return nil
}
