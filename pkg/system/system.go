package system

import (
	"crypto/rand"
	"encoding/hex"
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

// IsRoot 返回当前进程是否以 root (uid 0) 运行。
func IsRoot() bool {
	return os.Geteuid() == 0
}

// RequireRoot 检查当前进程是否以 root 运行，不是则返回错误。
// op 是操作名称，会出现在错误信息中。
func RequireRoot(op string) error {
	if IsRoot() {
		return nil
	}
	return fmt.Errorf("%s requires root (re-run with sudo)", op)
}

// RandomHex 生成 size 字节的随机数的十六进制字符串。
func RandomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}