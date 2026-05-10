package config

import (
	"os"
	"os/user"
	"path"
	"tinydocker/pkg/system"

	"go.yaml.in/yaml/v3"
)

const (
	defaultDataRoot = "/var/lib/tinydocker"
	defaultDns      = "8.8.8.8"
	defaultBip      = "172.17.0.1/16"
)

var defaultMirrors = []string{
	"docker.xuanyuan.me",
	"docker.m.daocloud.io",
	"dockerproxy.com",
	"hub-mirror.c.163.com",
	"mirror.baidubce.com",
}

var configPath = []string{
	"docker.yaml",
	"tinydocker.yaml",
}

func init() {
	// 默认配置
	C.DataRoot = defaultDataRoot
	C.Mirrors = defaultMirrors
	C.Dns = []string{defaultDns}
	C.Bip = defaultBip

	// 获取home目录
	u, err := user.Current()
	if err != nil {
		system.Panic("无法获取当前用户 err:%s", err.Error())
	}

	pathLen := len(configPath)
	for i := range pathLen {
		configPath = append(configPath, path.Join(u.HomeDir, configPath[i]))
	}

	for _, v := range configPath {
		info, err := os.Stat(v)
		if err != nil || info.IsDir() {
			continue
		}
		b, err := os.ReadFile(v)
		if err != nil {
			system.Panic("加载配置失败 err:%s", err.Error())
		}
		if err = yaml.Unmarshal(b, &C); err != nil {
			system.Panic("读取配置失败 err:%s", err.Error())
		}
		return
	}
}

type Config struct {
	DataRoot string   `yaml:"data-root"`
	Mirrors  []string `yaml:"mirrors"`
	Dns      []string `yaml:"dns"`
	Bip      string   `yaml:"bip"`
}

var C Config
