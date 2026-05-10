package system

import (
	"fmt"
	"os"
)

func Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("\033[31m%s\033[0m\n", msg)
}

func Panic(format string, args ...any) {
	Error(format, args...)
	os.Exit(1)
}
