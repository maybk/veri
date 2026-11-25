package credentials

import (
	"bytes"
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

// DefaultKeyFile 是跨平台主密钥在应用配置目录下的默认文件名。
const DefaultKeyFile = "secret.key"

const (
	masterKeySize     = 32
	maxMasterKeyBytes = 4096
)

var (
	// ErrMasterKeyMissing 表示凭据目录中缺少主密钥文件 secret.key。
	ErrMasterKeyMissing = errors.New("master key file missing")
	// ErrMasterKeyInvalid 表示主密钥文件 secret.key 格式无效、为空或长度不合法。
	ErrMasterKeyInvalid = errors.New("invalid master key file")
	// ErrDecryptFailed 表示主密钥与密文不匹配或密文已被篡改。
	ErrDecryptFailed = errors.New("decrypt credential failed")
)

var (
	utf8BOM              = []byte{0xEF, 0xBB, 0xBF}
	errKeyFileIncomplete = errors.New("master key file is being written")
)

// Sealer 使用保存在配置目录 secret.key 的跨平台 256 位主密钥提供并发安全的 AES-256-GCM 加解密。
type Sealer struct {
	mu        sync.Mutex
	keyPath   string
	cachedGCM cipher.AEAD
	keyInfo   os.FileInfo
}

// New 创建指向指定配置根目录的 Sealer。
func New(rootDir string) *Sealer {
	return &Sealer{keyPath: filepath.Join(rootDir, DefaultKeyFile)}
}

// KeyPath 返回主密钥文件路径。
func (v *Sealer) KeyPath() string {
	return v.keyPath
}

func (v *Sealer) cipher(createIfMissing bool) (cipher.AEAD, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cachedGCM != nil && v.keyInfo != nil {
		if info, err := os.Stat(v.keyPath); err == nil &&
			os.SameFile(info, v.keyInfo) &&
			info.ModTime().Equal(v.keyInfo.ModTime()) &&
			info.Size() == v.keyInfo.Size() {
			return v.cachedGCM, nil
		}
		v.cachedGCM = nil
		v.keyInfo = nil
	}
	key, info, err := v.loadKeyLocked(createIfMissing)
	if err != nil {
		return nil, err
	}
	// 使用完毕后立即归零原始主密钥切片，避免明文主密钥长期驻留内存。
	defer clear(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	v.cachedGCM = gcm
	v.keyInfo = info
	return gcm, nil
}

// Encrypt 使用主密钥将明文密封为带随机 96 位 Nonce 前缀的 AES-256-GCM Base64 密文；
// 若主密钥文件尚不存在，则原子生成新的跨平台 256 位主密钥。
func (v *Sealer) Encrypt(plaintext string) (string, error) {
	return v.encrypt(plaintext, true)
}

func (v *Sealer) encrypt(plaintext string, createIfMissing bool) (string, error) {
	gcm, err := v.cipher(createIfMissing)
	if err != nil {
		return "", err
	}
	//nolint:gosec // G407: NewGCMWithRandomNonce 要求传入 nil nonce，并在内部自动生成随机 96 位 nonce 前缀
	sealed := gcm.Seal(nil, nil, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 使用已有主密钥解封由 Encrypt 生成的 Base64 密文；
// 只读路径绝不会自动创建或删除主密钥文件。
func (v *Sealer) Decrypt(encoded string) (string, error) {
	gcm, err := v.cipher(false)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: invalid ciphertext encoding: %w", ErrDecryptFailed, err)
	}
	plain, err := gcm.Open(nil, nil, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrDecryptFailed, err)
	}
	return string(plain), nil
}

func (v *Sealer) loadKeyLocked(createIfMissing bool) ([]byte, os.FileInfo, error) {
	key, info, err := v.readKey(createIfMissing)
	if err == nil {
		return key, info, nil
	}
	if errors.Is(err, errKeyFileIncomplete) {
		key, info, err = v.waitForConcurrentKeyWriteLocked(createIfMissing)
		if err == nil {
			return key, info, nil
		}
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	if !createIfMissing {
		return nil, nil, fmt.Errorf("%w: %w", ErrMasterKeyMissing, err)
	}
	return v.createKeyLocked()
}

func (v *Sealer) createKeyLocked() ([]byte, os.FileInfo, error) {
	newKey := make([]byte, masterKeySize)
	if _, err := rand.Read(newKey); err != nil {
		return nil, nil, fmt.Errorf("generate master key: %w", err)
	}
	payload := base64.StdEncoding.EncodeToString(newKey) + "\n"

	dir := filepath.Dir(v.keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		clear(newKey)
		return nil, nil, err
	}

	// 先完整落盘并收紧同目录临时文件权限，再通过 os.Link 原子排他地绑定到 secret.key（绝不覆盖已有文件，也无权限暴露窗口）。
	tmp, err := os.CreateTemp(dir, ".secret.key.tmp-*")
	if err != nil {
		clear(newKey)
		return nil, nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		clear(newKey)
		return nil, nil, err
	}
	if _, err := tmp.WriteString(payload); err != nil {
		_ = tmp.Close()
		clear(newKey)
		return nil, nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		clear(newKey)
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		clear(newKey)
		return nil, nil, err
	}
	if err := restrictFilePermissions(tmpPath); err != nil {
		clear(newKey)
		return nil, nil, err
	}

	if err := os.Link(tmpPath, v.keyPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			clear(newKey)
			return v.waitForConcurrentKeyWriteLocked(true)
		}
		// 兼容不支持硬链接的文件系统，回退至排他创建。
		return v.createExclFallbackLocked(newKey, payload)
	}

	info, _ := os.Stat(v.keyPath)
	return newKey, info, nil
}

func (v *Sealer) createExclFallbackLocked(newKey []byte, payload string) ([]byte, os.FileInfo, error) {
	f, err := os.OpenFile(v.keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		clear(newKey)
		return v.waitForConcurrentKeyWriteLocked(true)
	}
	if err != nil {
		clear(newKey)
		return nil, nil, err
	}
	if _, err := f.WriteString(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(v.keyPath)
		clear(newKey)
		return nil, nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(v.keyPath)
		clear(newKey)
		return nil, nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(v.keyPath)
		clear(newKey)
		return nil, nil, err
	}
	if err := restrictFilePermissions(v.keyPath); err != nil {
		_ = os.Remove(v.keyPath)
		clear(newKey)
		return nil, nil, err
	}
	info, _ := os.Stat(v.keyPath)
	return newKey, info, nil
}

func (v *Sealer) waitForConcurrentKeyWriteLocked(allowStaleCleanup bool) ([]byte, os.FileInfo, error) {
	for range 20 {
		key, info, readErr := v.readKey(allowStaleCleanup)
		if readErr == nil {
			return key, info, nil
		}
		// 仅在检测到并发写入中的未完成空文件时等待重试，避免对确定性错误白等。
		if !errors.Is(readErr, errKeyFileIncomplete) {
			return nil, nil, readErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	key, info, readErr := v.readKey(allowStaleCleanup)
	if readErr == nil {
		return key, info, nil
	}
	if errors.Is(readErr, errKeyFileIncomplete) {
		return nil, nil, fmt.Errorf("%w: empty master key file", ErrMasterKeyInvalid)
	}
	return nil, nil, readErr
}

func (v *Sealer) readKey(allowStaleCleanup bool) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(v.keyPath)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("%w: path is a directory", ErrMasterKeyInvalid)
	}
	if info.Size() > maxMasterKeyBytes {
		return nil, nil, fmt.Errorf("%w: file exceeds %d bytes", ErrMasterKeyInvalid, maxMasterKeyBytes)
	}
	data, err := os.ReadFile(v.keyPath)
	if err != nil {
		return nil, nil, err
	}
	trimmed := strings.TrimSpace(string(stripUTF8BOM(data)))
	if trimmed == "" {
		if time.Since(info.ModTime()) > 5*time.Second {
			if allowStaleCleanup {
				_ = os.Remove(v.keyPath)
				return nil, nil, fs.ErrNotExist
			}
			return nil, nil, fmt.Errorf("%w: empty master key file", ErrMasterKeyInvalid)
		}
		return nil, nil, errKeyFileIncomplete
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: decode base64: %w", ErrMasterKeyInvalid, err)
	}
	if len(key) != masterKeySize {
		return nil, nil, fmt.Errorf("%w: expected %d-byte key, got %d bytes", ErrMasterKeyInvalid, masterKeySize, len(key))
	}
	return key, info, nil
}

func stripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}
