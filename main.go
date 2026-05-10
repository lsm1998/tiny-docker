package main

import (
	"os"
	"tinydocker/cli"
	_ "tinydocker/config"
	"tinydocker/pkg/system"
)

func main() {
	if len(os.Args) <= 1 {
		system.Panic("usage: tinydocker <command>")
	}
	manager := cli.NewCliManager()
	if err := manager.Run(os.Args[1:]); err != nil {
		system.Panic("%s", err.Error())
	}
}
