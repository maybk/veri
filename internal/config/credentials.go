package config

import (
	"net/url"
	"slices"
	"strings"

	"veri/clikit/redact"
	"veri/internal/spec"
)

// Credential 记录单个 Provider 解析后的密钥列表及其来源说明（不含明文展示）。
type Credential struct {
	Keys           []string
	Source         string // e.g. "env:DEEPSEEK_API_KEY", "secrets.json", "config", "missing"
	HasPlainInFile bool
}

func resolveCred(rawSpec string, envNames []string, vaultVal string, getenv func(string) string) (Credential, []string) {
	trimmed := strings.TrimSpace(rawSpec)
	plainKeys, specEnvKeys, specEnvSources, hasPlain := parseSpecItems(trimmed, getenv)
	vKeys := splitKeyList(vaultVal)

	var allKnown []string
	allKnown = append(allKnown, plainKeys...)
	allKnown = append(allKnown, specEnvKeys...)
	allKnown = append(allKnown, vKeys...)

	// 1. 进程或 MCP 客户端显式注入的标准环境变量始终最高优先（同时保留 HasPlainInFile 以便文件权限告警生效）。
	var winningEnvKeys []string
	winningSource := ""
	for _, envName := range envNames {
		if val := strings.TrimSpace(getenv(envName)); val != "" {
			keys := splitKeyList(val)
			allKnown = append(allKnown, keys...)
			if len(keys) > 0 && winningSource == "" {
				winningEnvKeys = keys
				winningSource = "env:" + envName
			}
		}
	}
	if winningSource != "" {
		return Credential{
			Keys:           winningEnvKeys,
			Source:         winningSource,
			HasPlainInFile: hasPlain,
		}, allKnown
	}

	// 2. 本地加密凭据库 secrets.json 次之。
	if len(vKeys) > 0 {
		return Credential{
			Keys:           vKeys,
			Source:         spec.SecretsFileName,
			HasPlainInFile: hasPlain,
		}, allKnown
	}

	// 3. 回退使用 config.toml 中的自定义 env:VAR 或明文 api_key。
	var combined []string
	var sources []string
	if len(specEnvKeys) > 0 {
		combined = append(combined, specEnvKeys...)
		sources = append(sources, specEnvSources...)
	}
	if len(plainKeys) > 0 {
		combined = append(combined, plainKeys...)
		sources = appendUnique(sources, "config")
	}
	if len(combined) == 0 {
		return Credential{Source: "missing", HasPlainInFile: hasPlain}, allKnown
	}
	return Credential{
		Keys:           combined,
		Source:         strings.Join(sources, "+"),
		HasPlainInFile: hasPlain,
	}, allKnown
}

func parseSpecItems(trimmed string, getenv func(string) string) (plainKeys, envKeys, envSources []string, hasPlain bool) {
	if trimmed == "" {
		return nil, nil, nil, false
	}
	for _, item := range splitKeyList(trimmed) {
		if len(item) > 4 && strings.EqualFold(item[:4], "env:") {
			envName := strings.TrimSpace(item[4:])
			if envName == "" {
				continue
			}
			val := strings.TrimSpace(getenv(envName))
			if parsed := splitKeyList(val); len(parsed) > 0 {
				envKeys = append(envKeys, parsed...)
				envSources = appendUnique(envSources, "env:"+envName)
			}
			continue
		}
		hasPlain = true
		plainKeys = append(plainKeys, item)
	}
	return plainKeys, envKeys, envSources, hasPlain
}

func splitKeyList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func maskProviderView(rawSpec string, cred Credential) string {
	if cred.Source == spec.SecretsFileName && len(cred.Keys) > 0 {
		masked := make([]string, 0, len(cred.Keys))
		for _, k := range cred.Keys {
			masked = append(masked, redact.MaskKey(k))
		}
		return "encrypted:" + strings.Join(masked, ",")
	}
	return maskSpec(rawSpec)
}

func maskSpec(spec string) string {
	items := splitKeyList(spec)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, redact.MaskKey(item))
	}
	return strings.Join(out, ",")
}

func sanitizeConfigURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return redact.SanitizeURL(trimmed)
}

func extractProxyPassword(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") && !strings.HasPrefix(trimmed, "//") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User == nil {
		return ""
	}
	pw, ok := u.User.Password()
	if !ok {
		return ""
	}
	return pw
}

func appendUnique(slice []string, val string) []string {
	if slices.Contains(slice, val) {
		return slice
	}
	return append(slice, val)
}
