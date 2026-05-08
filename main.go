package main

import (
	"io/fs"
	"os"
	"tinydocker/cli"
	"tinydocker/pkg/system"
)

const (
	def = "/var/lib/tinydocker/"
)

func init() {
	info, err := os.Stat(def)
	if err == os.ErrExist { // 创建目录
		if err = os.MkdirAll(def, fs.ModePerm); err != nil {
			system.Panic("创建容器目录失败，请检查权限，err=%s", err.Error())
		}
	} else if !info.IsDir() {
		system.Panic("存在和容器目录同名的文件，请先清理")
	}
}

func main() {
	if len(os.Args) <= 1 {
		system.Panic("缺少操作指令")
	}
	manager := cli.NewCliManager()
	manager.Run(os.Args[1:])
}
