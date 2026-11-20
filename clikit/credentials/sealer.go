package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultKeyFile 是本机主密钥在应用配置目录下的默认文件名。
const DefaultKeyFile = "secret.key"

// Sealer 使用保存在配置目录 secret.key 的 256 位主密钥提供并发安全的 AES-256-GCM 加解密。
type Sealer struct {
	mu        sync.Mutex
	keyPath   string
	cachedKey []byte
	cachedGCM cipher.AEAD
	keyInfo   os.FileInfo
}

// New 创建指向指定配置根目录的 Sealer。
func New(rootDir string) *Sealer {
	return &Sealer{keyPath: filepath.Join(rootDir, DefaultKeyFile)}
}

func (v *Sealer) cipher() (cipher.AEAD, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cachedGCM != nil && v.keyInfo != nil {
		if info, err := os.Stat(v.keyPath); err == nil &&
			os.SameFile(info, v.keyInfo) &&
			info.ModTime().Equal(v.keyInfo.ModTime()) {
			return v.cachedGCM, nil
		}
		v.cachedKey = nil
		v.cachedGCM = nil
		v.keyInfo = nil
	}
	key, err := v.loadOrCreateKeyLocked()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	v.cachedGCM = gcm
	return gcm, nil
}

// Encrypt 使用本机主密钥将明文密封为带随机 96 位 Nonce 前缀的 AES-256-GCM Base64 密文。
func (v *Sealer) Encrypt(plaintext string) (string, error) {
	gcm, err := v.cipher()
	if err != nil {
		return "", err
	}
	//nolint:gosec // G407: NewGCMWithRandomNonce 要求传入 nil nonce，并在内部自动生成随机 96 位 nonce 前缀
	sealed := gcm.Seal(nil, nil, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解封由 Encrypt 生成的 Base64 密文。
func (v *Sealer) Decrypt(encoded string) (string, error) {
	gcm, err := v.cipher()
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid credential ciphertext: %w", err)
	}
	plain, err := gcm.Open(nil, nil, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt credential failed: %w", err)
	}
	return string(plain), nil
}

var errKeyFileIncomplete = errors.New("master key file is being written")

func (v *Sealer) loadOrCreateKeyLocked() ([]byte, error) {
	if len(v.cachedKey) == 32 {
		return v.cachedKey, nil
	}
	if key, info, err := v.readKey(); err == nil {
		v.cachedKey = key
		v.keyInfo = info
		return key, nil
	} else if errors.Is(err, errKeyFileIncomplete) {
		return v.waitForConcurrentKeyWriteLocked()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	sealedKey, err := sealKeyBytes(newKey)
	if err != nil {
		return nil, fmt.Errorf("protect master key: %w", err)
	}
	payload := base64.StdEncoding.EncodeToString(sealedKey) + "\n"

	dir := filepath.Dir(v.keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	// 先完整落盘同目录临时文件，再通过 os.Link 原子且排他地绑定到 secret.key（永不覆盖已有文件，也绝不产生 0 字节残缺文件）。
	tmp, err := os.CreateTemp(dir, ".secret.key.tmp-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.WriteString(payload); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	if err := os.Link(tmpPath, v.keyPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return v.waitForConcurrentKeyWriteLocked()
		}
		// 兼容不支持硬链接的文件系统回退至排他创建
		return v.createExclFallbackLocked(newKey, payload)
	}

	if info, statErr := os.Stat(v.keyPath); statErr == nil {
		v.keyInfo = info
	}
	v.cachedKey = newKey
	return newKey, nil
}

func (v *Sealer) createExclFallbackLocked(newKey []byte, payload string) ([]byte, error) {
	f, err := os.OpenFile(v.keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return v.waitForConcurrentKeyWriteLocked()
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(v.keyPath)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(v.keyPath)
		return nil, err
	}
	info, statErr := f.Stat()
	if err := f.Close(); err != nil {
		_ = os.Remove(v.keyPath)
		return nil, err
	}
	v.cachedKey = newKey
	if statErr == nil {
		v.keyInfo = info
	}
	return newKey, nil
}

func (v *Sealer) waitForConcurrentKeyWriteLocked() ([]byte, error) {
	for range 20 {
		if key, info, readErr := v.readKey(); readErr == nil {
			v.cachedKey = key
			v.keyInfo = info
			return key, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	key, info, readErr := v.readKey()
	if readErr == nil {
		v.cachedKey = key
		v.keyInfo = info
	}
	return key, readErr
}

func (v *Sealer) readKey() ([]byte, os.FileInfo, error) {
	info, err := os.Stat(v.keyPath)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(v.keyPath)
	if err != nil {
		return nil, nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		if time.Since(info.ModTime()) > 5*time.Second {
			_ = os.Remove(v.keyPath)
			return nil, nil, fs.ErrNotExist
		}
		return nil, nil, errKeyFileIncomplete
	}
	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, nil, fmt.Errorf("decode master key: %w", err)
	}
	key, err := unsealKeyBytes(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("unseal master key: %w", err)
	}
	if len(key) != 32 {
		return nil, nil, errors.New("invalid master key length")
	}
	return key, info, nil
}
