// Package main 是 veri 单二进制可执行程序入口，通过 go:embed 注入静态资产并启动命令行或 MCP 服务。
package main

import (
	"embed"
	"os"

	"veri/clikit/buildinfo"
	"veri/internal/cli"
)

//go:embed build/config.yml skills/veri/SKILL.md
var embeddedAssets embed.FS

var (
	version   = ""
	commit    = "none"
	buildDate = ""
)

func main() {
	info := buildinfo.Resolve(embeddedAssets, version, commit, buildDate)
	os.Exit(cli.Execute(os.Args[1:], embeddedAssets, info, os.Stdin, os.Stdout, os.Stderr))
}
