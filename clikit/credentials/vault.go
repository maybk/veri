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

const (
	secretSchemaVersion = 1
	maxVaultFileBytes   = 1 << 20 // 单个 secrets.json 上限（1 MiB），防止误置超大文件引发内存抖动
)

var (
	// ErrNotFound 表示目标命名凭据尚未在加密凭据库中保存。
	ErrNotFound = errors.New("secret not found")
	// ErrVaultCorrupted 表示加密凭据库 secrets.json 结构损坏或版本不受支持。
	ErrVaultCorrupted = errors.New("corrupted credentials vault")
)

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

// KeyPath 返回加密主密钥文件路径。
func (v *Vault) KeyPath() string {
	return v.sealer.KeyPath()
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
	// 库中若存在除目标键外的其他加密条目，必须复用既有主密钥，禁止自动新建主密钥造成双密钥混写损坏。
	createKeyIfMissing := !hasOtherKey(entries, key)
	sealed, err := v.sealer.encrypt(trimmed, createKeyIfMissing)
	if err != nil {
		return err
	}
	entries[key] = sealed
	return v.writeMapLocked(entries)
}

// SetMany 校验、加密并单次原子写入多条命名凭据（全量成功或保持原样不变）。
func (v *Vault) SetMany(items map[string]string) error {
	if len(items) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(items))
	for rawName, rawSecret := range items {
		key, err := normalizeVaultKey(rawName)
		if err != nil {
			return err
		}
		trimmed := strings.TrimSpace(rawSecret)
		if trimmed == "" {
			return fmt.Errorf("secret value for %s must not be empty", key)
		}
		if prev, dup := normalized[key]; dup && prev != trimmed {
			return fmt.Errorf("conflicting values for credential %s", key)
		}
		normalized[key] = trimmed
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.readMapLocked()
	if err != nil {
		return err
	}
	createKeyIfMissing := !hasOtherEntries(entries, normalized)
	sealedBatch := make(map[string]string, len(normalized))
	for _, key := range slices.Sorted(maps.Keys(normalized)) {
		sealed, encErr := v.sealer.encrypt(normalized[key], createKeyIfMissing)
		if encErr != nil {
			return encErr
		}
		sealedBatch[key] = sealed
	}
	maps.Copy(entries, sealedBatch)
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

func hasOtherKey(existing map[string]string, target string) bool {
	if len(existing) == 0 {
		return false
	}
	if len(existing) > 1 {
		return true
	}
	_, exists := existing[target]
	return !exists
}

func hasOtherEntries(existing, overwriting map[string]string) bool {
	for key := range existing {
		if _, ok := overwriting[key]; !ok {
			return true
		}
	}
	return false
}

func normalizeVaultKey(raw string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "", errors.New("credential name must not be empty")
	}
	return key, nil
}

func (v *Vault) readMapLocked() (map[string]string, error) {
	data, err := storage.ReadRegularFileBounded(v.secretsPath, maxVaultFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		if errors.Is(err, os.ErrInvalid) || errors.Is(err, storage.ErrFileTooLarge) {
			return nil, fmt.Errorf("%w: %s: %w", ErrVaultCorrupted, DefaultSecretsFile, err)
		}
		return nil, err
	}
	trimmed := bytes.TrimSpace(storage.StripUTF8BOM(data))
	if len(trimmed) == 0 {
		return map[string]string{}, nil
	}

	var rawFields map[string]jsontext.Value
	if err := json.Unmarshal(trimmed, &rawFields); err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", ErrVaultCorrupted, DefaultSecretsFile, err)
	}
	verRaw, hasVer := rawFields["schemaVersion"]
	dataRaw, hasData := rawFields["data"]
	if !hasVer || !hasData || len(rawFields) != 2 {
		return nil, fmt.Errorf("%w: invalid %s envelope structure", ErrVaultCorrupted, DefaultSecretsFile)
	}
	var ver int
	if err := json.Unmarshal(verRaw, &ver); err != nil || ver != secretSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported %s schemaVersion %d", ErrVaultCorrupted, DefaultSecretsFile, ver)
	}
	var m map[string]string
	if err := json.Unmarshal(dataRaw, &m); err != nil {
		return nil, fmt.Errorf("%w: parse %s payload: %w", ErrVaultCorrupted, DefaultSecretsFile, err)
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
	return storage.WritePrivateFile(v.secretsPath, append(encoded, '\n'))
}
