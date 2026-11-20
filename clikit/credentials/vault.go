package credentials

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"veri/clikit/storage"
)

// DefaultSecretsFile 是版本化加密凭据库在应用配置目录下的默认文件名。
const DefaultSecretsFile = "secrets.json"

const secretSchemaVersion = 1

// ErrNotFound 表示目标命名凭据尚未在加密凭据库中保存。
var ErrNotFound = errors.New("secret not found")

type versionedDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	Data          map[string]string `json:"data"`
}

// Vault 使用 Sealer 将命名凭据加密存储在 secrets.json 中，与具体业务领域完全解耦。
type Vault struct {
	mu          sync.Mutex
	secretsPath string
	sealer      *Sealer
}

// NewVault 构造指向指定配置目录（如 ~/.config/<app>）的加密凭据库。
func NewVault(rootDir string) *Vault {
	return &Vault{
		secretsPath: filepath.Join(rootDir, DefaultSecretsFile),
		sealer:      New(rootDir),
	}
}

// SecretsPath 返回加密凭据文件路径。
func (v *Vault) SecretsPath() string {
	return v.secretsPath
}

// Set 加密并原子写入指定命名键（大小写归一为小写）的敏感值。
func (v *Vault) Set(name, secret string) error {
	key, err := normalizeVaultKey(name)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return errors.New("secret value must not be empty")
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return err
	}
	sealed, err := v.sealer.Encrypt(trimmed)
	if err != nil {
		return err
	}
	entries[key] = sealed
	return v.writeMapLocked(entries)
}

// Get 读取并解封指定命名键的已加密敏感值；不存在时返回 ErrNotFound。
func (v *Vault) Get(name string) (string, error) {
	key, err := normalizeVaultKey(name)
	if err != nil {
		return "", ErrNotFound
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return "", err
	}
	sealed, exists := entries[key]
	if !exists {
		return "", ErrNotFound
	}
	return v.sealer.Decrypt(sealed)
}

// LoadAll 读取并解封凭据库内全部已保存的命名键值映射。
func (v *Vault) LoadAll() (map[string]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for name, sealed := range entries {
		plain, decErr := v.sealer.Decrypt(sealed)
		if decErr != nil {
			return nil, fmt.Errorf("decrypt stored secret for %s: %w", name, decErr)
		}
		out[name] = plain
	}
	return out, nil
}

// Delete 从凭据库中移除指定命名键；不存在时返回 (false, nil)。
func (v *Vault) Delete(name string) (bool, error) {
	key, err := normalizeVaultKey(name)
	if err != nil {
		return false, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return false, err
	}
	if _, exists := entries[key]; !exists {
		return false, nil
	}
	delete(entries, key)
	if err := v.writeMapLocked(entries); err != nil {
		return false, err
	}
	return true, nil
}

// ListKeys 按字典序返回已在加密凭据库中保存条目的命名键列表。
func (v *Vault) ListKeys() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(entries)), nil
}

func normalizeVaultKey(raw string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "", errors.New("credential name must not be empty")
	}
	return key, nil
}

func (v *Vault) readMapLocked() (map[string]string, error) {
	data, err := os.ReadFile(v.secretsPath)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return map[string]string{}, nil
	}

	var rawFields map[string]jsontext.Value
	if err := json.Unmarshal(trimmed, &rawFields); err != nil {
		return nil, fmt.Errorf("parse %s: %w", DefaultSecretsFile, err)
	}
	verRaw, hasVer := rawFields["schemaVersion"]
	dataRaw, hasData := rawFields["data"]
	if !hasVer || !hasData || len(rawFields) != 2 {
		return nil, fmt.Errorf("invalid %s envelope structure", DefaultSecretsFile)
	}
	var ver int
	if err := json.Unmarshal(verRaw, &ver); err != nil || ver != secretSchemaVersion {
		return nil, fmt.Errorf("unsupported %s schemaVersion %d", DefaultSecretsFile, ver)
	}
	var m map[string]string
	if err := json.Unmarshal(dataRaw, &m); err != nil {
		return nil, fmt.Errorf("parse %s payload: %w", DefaultSecretsFile, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func (v *Vault) writeMapLocked(m map[string]string) error {
	doc := versionedDocument{
		SchemaVersion: secretSchemaVersion,
		Data:          m,
	}
	encoded, err := json.Marshal(doc, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(v.secretsPath, append(encoded, '\n'), 0o700, 0o600)
}
