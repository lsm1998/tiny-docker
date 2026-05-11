package cli

import (
	"fmt"
	"slices"
	"strings"
)

func init() {
	entry := &cmdEntry{
		exec:        helpExec,
		description: "Show this help message",
	}
	registerCli("help", entry)
	registerCli("?", entry)
}

func helpExec(args ...string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: tinydocker help")
	}
	fmt.Println("Available commands:")

	var msgList = make([]string, 0, len(cliMap))
	for k, v := range cliMap {
		if strings.HasPrefix(k, "_") {
			continue
		}
		msgList = append(msgList, fmt.Sprintf("  %-10s %s\n", k, v.Description()))
	}

	slices.Sort(msgList)
	for _, v := range msgList {
		fmt.Print(v)
	}
	return nil
}