package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"veri/clikit/credentials"
	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

type dotEnvItem struct {
	Name  string
	Value string
	Line  int
}

type envVarTarget struct {
	provider string
	priority int // 0 为主变量，数值越小优先级越高
}

// ImportKeysFromEnvFile 从 .env 文件或进程替换 FIFO 批量读取标准 Provider 凭据并加密写入 secrets.json。
func ImportKeysFromEnvFile(cfgDir, envPath string, opts ImportOptions) (ImportResult, error) {
	safePath, secretHints := scrubImportPath(envPath)
	fi, err := os.Stat(envPath)
	if err != nil {
		return ImportResult{}, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("cannot read %q", safePath), errors.New(redact.Scrub(err.Error(), secretHints))).
			WithMsgKey("err.key_import_read_failed").
			WithParam("path", safePath)
	}
	if fi.IsDir() {
		return ImportResult{}, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("import path %q is a directory", safePath)).
			WithMsgKey("err.key_import_is_dir").
			WithParam("path", safePath)
	}
	if fi.Mode().IsRegular() && fi.Size() > maxConfigFileBytes {
		return ImportResult{}, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("import input %q exceeds %d bytes", safePath, maxConfigFileBytes)).
			WithMsgKey("err.key_import_too_large").
			WithParam("source", safePath)
	}
	f, err := os.Open(filepath.Clean(envPath)) // #nosec G304 -- local CLI user-specified .env file path
	if err != nil {
		return ImportResult{}, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("cannot read %q", safePath), errors.New(redact.Scrub(err.Error(), secretHints))).
			WithMsgKey("err.key_import_read_failed").
			WithParam("path", safePath)
	}
	defer func() { _ = f.Close() }()
	return importKeysFromLimitedReader(cfgDir, "env_file", envPath, safePath, secretHints, f, opts)
}

// ImportKeysFromEnvReader 从流（如 stdin）批量读取 .env 内容并加密写入 secrets.json。
func ImportKeysFromEnvReader(cfgDir, source string, r io.Reader, opts ImportOptions) (ImportResult, error) {
	return importKeysFromLimitedReader(cfgDir, source, "", source, nil, r, opts)
}

func importKeysFromLimitedReader(cfgDir, source, sourcePath, displaySource string, secretHints []string, r io.Reader, opts ImportOptions) (ImportResult, error) {
	limited, err := io.ReadAll(io.LimitReader(r, maxConfigFileBytes+1))
	if err != nil {
		return ImportResult{}, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("failed to read %s", displaySource), errors.New(redact.Scrub(err.Error(), secretHints))).
			WithMsgKey("err.key_import_read_failed").
			WithParam("path", displaySource)
	}
	if len(limited) > maxConfigFileBytes {
		return ImportResult{}, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("import input %q exceeds %d bytes", displaySource, maxConfigFileBytes)).
			WithMsgKey("err.key_import_too_large").
			WithParam("source", displaySource)
	}
	return ImportKeysFromEnv(cfgDir, source, sourcePath, string(limited), opts)
}

// ImportKeysFromEnv 解析 .env 文本中的已知 Provider 环境变量并批量加密写入 secrets.json。
func ImportKeysFromEnv(cfgDir, source, sourcePath, raw string, opts ImportOptions) (ImportResult, error) {
	vault := credentials.NewVault(cfgDir)
	res := ImportResult{
		Source:      source,
		SourcePath:  sourcePath,
		SecretsPath: vault.SecretsPath(),
		DryRun:      opts.DryRun,
		Entries:     []ImportEntry{},
	}

	errSource := source
	if sourcePath != "" {
		errSource = sourcePath
	}
	items, err := parseDotEnv(raw, errSource)
	if err != nil {
		return res, err
	}

	existingVault, err := loadVaultSecrets(vault, cfgDir)
	if err != nil {
		return res, err
	}

	type candidate struct {
		envName  string
		priority int
		keys     []string
	}
	envIndex := credentialEnvIndexCached()
	selected := make(map[string]candidate)
	ignoredSet := make(map[string]struct{})

	for _, item := range items {
		upperName := strings.ToUpper(item.Name)
		target, ok := envIndex[upperName]
		if !ok {
			ignoredSet[item.Name] = struct{}{}
			continue
		}
		keys := splitKeyList(item.Value)
		if len(keys) == 0 {
			continue
		}
		// 主变量优先级高于兼容别名；同优先级按文件后出现项覆盖前者。
		if cur, exists := selected[target.provider]; !exists || target.priority <= cur.priority {
			selected[target.provider] = candidate{
				envName:  upperName,
				priority: target.priority,
				keys:     keys,
			}
		}
	}

	if len(ignoredSet) > 0 {
		res.IgnoredKeys = make([]string, 0, len(ignoredSet))
		for k := range ignoredSet {
			res.IgnoredKeys = append(res.IgnoredKeys, k)
		}
		slices.Sort(res.IgnoredKeys)
	}

	toWrite := make(map[string]string, len(selected))
	for _, prov := range spec.CredentialProviders() {
		cand, ok := selected[prov]
		if !ok {
			continue
		}
		action, reason, shouldWrite, _ := planImportAction(cand.keys, existingVault[prov], opts, false)
		if shouldWrite {
			toWrite[prov] = strings.Join(cand.keys, ",")
		}
		res.addEntry(ImportEntry{
			Provider: prov,
			Key:      cand.envName,
			Action:   action,
			Reason:   reason,
			Masked:   maskKeyList(cand.keys),
			KeyCount: len(cand.keys),
		})
	}

	if opts.DryRun {
		return res, nil
	}
	if err := saveAndVerifyVaultBatch(vault, toWrite); err != nil {
		return res, err
	}
	return res, nil
}

// credentialEnvIndexCached 惰性缓存标准凭据环境变量（大写）到目标 Provider 与优先级的只读映射表。
var credentialEnvIndexCached = sync.OnceValue(func() map[string]envVarTarget {
	providers := spec.Providers()
	index := make(map[string]envVarTarget, len(providers)*2)
	for _, info := range providers {
		if !info.NeedsCredential() {
			continue
		}
		for prio, envName := range info.EnvVars {
			index[strings.ToUpper(envName)] = envVarTarget{
				provider: info.Name,
				priority: prio,
			}
		}
	}
	return index
})

// parseDotEnv 解析标准 .env 文本（支持 UTF-8 BOM、CRLF、可选 export 前缀、单双引号与行尾注释）。
// 语法错误仅报告来源与行号，绝不回显原始行内容，防止在报错信息中泄露明文凭据。
func parseDotEnv(raw, source string) ([]dotEnvItem, error) {
	normalized := normalizeNewlines(raw)
	if normalized == "" {
		return nil, nil
	}
	lines := strings.Split(normalized, "\n")
	items := make([]dotEnvItem, 0, len(lines))
	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if rest, ok := stripDotEnvExportPrefix(trimmed); ok {
			trimmed = rest
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			return nil, invalidDotEnvError(source, lineNo)
		}
		name := strings.TrimSpace(trimmed[:eq])
		if !isValidEnvVarName(name) {
			return nil, invalidDotEnvError(source, lineNo)
		}
		valPart := strings.TrimLeft(trimmed[eq+1:], " \t")
		val, ok := parseDotEnvValue(valPart)
		if !ok {
			return nil, invalidDotEnvError(source, lineNo)
		}
		items = append(items, dotEnvItem{
			Name:  name,
			Value: val,
			Line:  lineNo,
		})
	}
	return items, nil
}

func stripDotEnvExportPrefix(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "export") || len(trimmed) <= len("export") {
		return trimmed, false
	}
	next := trimmed[len("export")]
	if next != ' ' && next != '\t' {
		return trimmed, false
	}
	return strings.TrimSpace(trimmed[len("export"):]), true
}

func parseDotEnvValue(valPart string) (string, bool) {
	if valPart == "" {
		return "", true
	}
	switch valPart[0] {
	case '"':
		return parseDoubleQuotedDotEnvValue(valPart)
	case '\'':
		return parseSingleQuotedDotEnvValue(valPart)
	default:
		return strings.TrimSpace(stripUnquotedDotEnvComment(valPart)), true
	}
}

func parseDoubleQuotedDotEnvValue(valPart string) (string, bool) {
	var b strings.Builder
	b.Grow(len(valPart))
	escaped := false
	for i := 1; i < len(valPart); i++ {
		c := valPart[i]
		if escaped {
			switch c {
			case '"', '\\', '$':
				b.WriteByte(c)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte('\\')
				b.WriteByte(c)
			}
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			rest := strings.TrimSpace(valPart[i+1:])
			if rest != "" && !strings.HasPrefix(rest, "#") {
				return "", false
			}
			return b.String(), true
		}
		b.WriteByte(c)
	}
	return "", false
}

func parseSingleQuotedDotEnvValue(valPart string) (string, bool) {
	closeIdx := strings.IndexByte(valPart[1:], '\'')
	if closeIdx < 0 {
		return "", false
	}
	val := valPart[1 : 1+closeIdx]
	rest := strings.TrimSpace(valPart[2+closeIdx:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false
	}
	return val, true
}

// stripUnquotedDotEnvComment 仅剥离行首或空白后的 '#' 行尾注释，保留值内部无空格分隔的 '#' 字符。
func stripUnquotedDotEnvComment(valPart string) string {
	for i := range len(valPart) {
		if valPart[i] == '#' && (i == 0 || valPart[i-1] == ' ' || valPart[i-1] == '\t') {
			return valPart[:i]
		}
	}
	return valPart
}

func invalidDotEnvError(source string, lineNo int) *core.Error {
	safeSource, _ := scrubImportPath(source)
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid .env syntax in %s at line %d", safeSource, lineNo)).
		WithMsgKey("err.key_import_invalid_env").
		WithParam("source", safeSource).
		WithParam("line", strconv.Itoa(lineNo))
}

// scrubImportPath 识别并遮蔽路径中疑似误传的原始 API Key 片段（兼容 Unix '/' 与 Windows '\'），避免报错回显明文凭据。
func scrubImportPath(rawPath string) (string, []string) {
	var secretHints []string
	if redact.LooksLikeSecret(rawPath) {
		secretHints = append(secretHints, rawPath)
	} else {
		for _, seg := range strings.FieldsFunc(rawPath, func(r rune) bool { return r == '/' || r == '\\' }) {
			if redact.LooksLikeSecret(seg) {
				secretHints = append(secretHints, seg)
			}
		}
	}
	return redact.Scrub(rawPath, secretHints), secretHints
}
