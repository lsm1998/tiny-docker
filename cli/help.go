package cli

import (
	"fmt"
	"slices"
)

func init() {
	registerCli("help", &HelpCli{})
	registerCli("?", &HelpCli{})
}

type HelpCli struct {
}

func (*HelpCli) Exec(args ...string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: tinydocker help")
	}
	fmt.Println("Available commands:")

	var msgList = make([]string, 0, len(cliMap))
	for k, v := range cliMap {
		msgList = append(msgList, fmt.Sprintf("  %-10s %s\n", k, v.Description()))
	}

	// 保证打印顺序
	slices.Sort(msgList)
	for _, v := range msgList {
		fmt.Print(v)
	}
	return nil
}

func (*HelpCli) Description() string {
	return "Show this help message"
}
