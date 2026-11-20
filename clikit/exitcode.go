package clikit

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// 标准命令行进程退出码约定（0 成功，1 内部错误，2 参数用法错误，3 配置/鉴权/配额不可用，4 限流/超时/上游故障，5 策略拦截/目标内容不可用，130 信号中断）。
const (
	ExitOK          = 0   // 执行成功
	ExitFailure     = 1   // 程序内部错误或综合诊断未通过
	ExitUsage       = 2   // 命令行参数、配置语法或本地用法错误
	ExitConfig      = 3   // 无可用后端、凭据鉴权失败、服务未开通或账户余额不足
	ExitUnavailable = 4   // 触发限流、网络超时或上游服务不可用
	ExitBlocked     = 5   // 访问策略拦截或目标内容不可用
	ExitInterrupted = 130 // 收到 SIGINT / SIGTERM 信号中断（128 + 2）
)

// NotifyContext 返回绑定了操作系统中断信号（SIGINT / SIGTERM）的 Context。
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// ScanFlagValue 在完整命令树解析前轻量预扫描指定长选项的值；遇到 "--" 分隔符时停止。
func ScanFlagValue(args []string, flagName string) string {
	name := strings.TrimLeft(strings.TrimSpace(flagName), "-")
	if name == "" {
		return ""
	}
	exact := "--" + name
	prefix := exact + "="
	var val string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == exact && i+1 < len(args) {
			val = args[i+1]
			i++
		} else if after, ok := strings.CutPrefix(arg, prefix); ok {
			val = after
		}
	}
	return val
}

// HasBoolFlag 在完整命令树解析前预扫描是否存在指定布尔长选项（如 "--json"）。
func HasBoolFlag(args []string, flagName string) bool {
	name := strings.TrimLeft(strings.TrimSpace(flagName), "-")
	if name == "" {
		return false
	}
	exact := "--" + name
	prefix := exact + "="
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == exact {
			return true
		}
		if after, ok := strings.CutPrefix(arg, prefix); ok {
			v := strings.ToLower(strings.TrimSpace(after))
			return v != "false" && v != "0"
		}
	}
	return false
}
