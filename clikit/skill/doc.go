// Package skill 从静态资产 fs.FS 读取内嵌 SKILL.md，并跨平台原子安装至开放标准 .agents/skills 目录或指定自定义目录。
//
// 依赖边界：仅依赖 Go 标准库与 clikit/storage，绝不引用上层业务包。
package skill
