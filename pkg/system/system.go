package system

import (
	"fmt"
	"os"
)

func Panic(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("\033[31m%s\033[0m\n", msg)
	os.Exit(1)
}
