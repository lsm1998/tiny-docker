package main

import (
	"os"
	"tinydocker/cli"
	"tinydocker/pkg/system"
)

func main() {
	if len(os.Args) <= 1 {
		system.Panic("缺少操作指令")
	}
	manager := cli.NewCliManager()
	if err := manager.Run(os.Args[1:]); err != nil {
		system.Panic("%s", err.Error())
	}
}
