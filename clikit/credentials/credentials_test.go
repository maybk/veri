package credentials

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"veri/clikit/storage"
)

func TestVaultLifecycleAndConcurrency(t *testing.T) {
	root := t.TempDir()
	vault := NewVault(root)

	if err := vault.Set("Alpha", "sk-secret-alpha-1111"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	rawDisk, err := os.ReadFile(vault.SecretsPath())
	if err != nil || strings.Contains(string(rawDisk), "secret-alpha") {
		t.Fatalf("secrets.json missing or leaked plaintext: err=%v raw=%s", err, string(rawDisk))
	}
	if runtime.GOOS != "windows" {
		keyInfo, sErr := os.Stat(filepath.Join(root, DefaultKeyFile))
		if sErr != nil || keyInfo.Mode().Perm() != 0o600 {
			t.Fatalf("expected 0600 secret.key, got err=%v mode=%v", sErr, keyInfo.Mode())
		}
	}

	got, err := vault.Get("alpha")
	if err != nil || got != "sk-secret-alpha-1111" {
		t.Fatalf("Get(alpha) = (%q, %v)", got, err)
	}

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			v := NewVault(root)
			_ = v.Set("concurrent", "val")
			_, _ = v.LoadAll()
		})
	}
	wg.Wait()

	keys, err := vault.ListKeys()
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListKeys = (%v, %v), want 2 keys", keys, err)
	}

	removed, err := vault.Delete("alpha")
	if err != nil || !removed {
		t.Fatalf("Delete(alpha) = (%v, %v)", removed, err)
	}
	if _, err := vault.Get("alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Delete, got %v", err)
	}
}

func TestSealerRandomNonceAndTamperRejection(t *testing.T) {
	sealer := New(t.TempDir())

	c1, err := sealer.Encrypt("sk-same-secret-value")
	if err != nil {
		t.Fatalf("Encrypt #1 failed: %v", err)
	}
	c2, err := sealer.Encrypt("sk-same-secret-value")
	if err != nil {
		t.Fatalf("Encrypt #2 failed: %v", err)
	}
	if c1 == c2 {
		t.Fatal("expected random nonce to produce distinct ciphertexts for identical plaintext")
	}

	plain, err := sealer.Decrypt(c1)
	if err != nil || plain != "sk-same-secret-value" {
		t.Fatalf("Decrypt = (%q, %v), want original plaintext", plain, err)
	}

	// 篡改密文或截断短于 nonce+tag 长度均必须报错拒绝。
	tampered := "A" + c1[1:]
	if c1[0] == 'A' {
		tampered = "B" + c1[1:]
	}
	if _, err := sealer.Decrypt(tampered); err == nil {
		t.Fatal("expected tampered ciphertext to fail authentication")
	}
	if _, err := sealer.Decrypt("c2hvcnQ="); err == nil {
		t.Fatal("expected truncated ciphertext to be rejected")
	}
}

func TestVaultSetManyAtomicityAndValidation(t *testing.T) {
	root := t.TempDir()
	vault := NewVault(root)

	// 空批量写入不应产生任何磁盘副作用。
	if err := vault.SetMany(nil); err != nil {
		t.Fatalf("SetMany(nil) error: %v", err)
	}
	if _, err := os.Stat(vault.SecretsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected empty SetMany not to create secrets.json, stat err=%v", err)
	}

	if err := vault.Set("existing", "keep-me-1234"); err != nil {
		t.Fatalf("seed Set failed: %v", err)
	}

	// 包含空值或大小写冲突时必须整批拒绝，原有记录不受影响。
	if err := vault.SetMany(map[string]string{
		"alpha": "sk-alpha-1111",
		"beta":  "   ",
	}); err == nil {
		t.Fatal("expected SetMany with empty secret to fail")
	}
	if err := vault.SetMany(map[string]string{
		"Gamma": "sk-gamma-1",
		"gamma": "sk-gamma-2",
	}); err == nil {
		t.Fatal("expected SetMany with case-folded conflicting values to fail")
	}
	if keys, err := vault.ListKeys(); err != nil || len(keys) != 1 || keys[0] != "existing" {
		t.Fatalf("failed batch should not mutate vault, got keys=%v err=%v", keys, err)
	}

	// 正常批量写入应单次落盘并保留既有条目，磁盘绝无明文。
	if err := vault.SetMany(map[string]string{
		"Alpha": "sk-alpha-1111",
		"beta":  "sk-beta-2222,sk-beta-3333",
	}); err != nil {
		t.Fatalf("SetMany failed: %v", err)
	}
	rawDisk, err := os.ReadFile(vault.SecretsPath())
	if err != nil {
		t.Fatalf("read secrets.json: %v", err)
	}
	for _, secret := range []string{"keep-me-1234", "sk-alpha-1111", "sk-beta-2222", "sk-beta-3333"} {
		if strings.Contains(string(rawDisk), secret) {
			t.Fatalf("secrets.json leaked plaintext %q", secret)
		}
	}
	all, err := vault.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if all["existing"] != "keep-me-1234" || all["alpha"] != "sk-alpha-1111" || all["beta"] != "sk-beta-2222,sk-beta-3333" {
		t.Fatalf("unexpected decrypted batch: %+v", all)
	}
}

func TestCrossPlatformPortableMasterKeyAndBOMCRLF(t *testing.T) {
	srcDir := t.TempDir()
	srcVault := NewVault(srcDir)
	want := map[string]string{
		"alpha": "sk-alpha-1111",
		"beta":  "sk-beta-2222,sk-beta-3333",
		"gamma": "sk-gamma-4444",
	}
	if err := srcVault.SetMany(want); err != nil {
		t.Fatalf("seed SetMany failed: %v", err)
	}

	// 验证落盘主密钥为三端通用的 32 字节标准 Base64，不含任何平台绑定封装。
	rawKeyFile, err := os.ReadFile(srcVault.KeyPath())
	if err != nil {
		t.Fatalf("read secret.key: %v", err)
	}
	decodedKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(rawKeyFile)))
	if err != nil || len(decodedKey) != masterKeySize {
		t.Fatalf("expected portable 32-byte base64 secret.key, got len=%d err=%v", len(decodedKey), err)
	}

	rawSecretsFile, err := os.ReadFile(srcVault.SecretsPath())
	if err != nil {
		t.Fatalf("read secrets.json: %v", err)
	}

	// 模拟跨平台复制至另一设备，并注入常见 Windows UTF-8 BOM 与 CRLF 换行符。
	dstDir := t.TempDir()
	bom := []byte{0xEF, 0xBB, 0xBF}
	keyWithBOMCRLF := append(append([]byte{}, bom...), bytes.ReplaceAll(rawKeyFile, []byte("\n"), []byte("\r\n"))...)
	secretsWithBOMCRLF := append(append([]byte{}, bom...), bytes.ReplaceAll(rawSecretsFile, []byte("\n"), []byte("\r\n"))...)
	if err := os.WriteFile(filepath.Join(dstDir, DefaultKeyFile), keyWithBOMCRLF, 0o600); err != nil {
		t.Fatalf("write copied secret.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, DefaultSecretsFile), secretsWithBOMCRLF, 0o600); err != nil {
		t.Fatalf("write copied secrets.json: %v", err)
	}

	dstVault := NewVault(dstDir)
	gotAll, err := dstVault.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after cross-device copy failed: %v", err)
	}
	for k, v := range want {
		if gotAll[k] != v {
			t.Fatalf("copied vault[%s] = %q, want %q", k, gotAll[k], v)
		}
	}

	// 在复制后的目录中继续追加写入新键应复用原主密钥，全部条目保持可读。
	if err := dstVault.Set("delta", "sk-delta-5555"); err != nil {
		t.Fatalf("Set on copied vault failed: %v", err)
	}
	if got, err := dstVault.Get("delta"); err != nil || got != "sk-delta-5555" {
		t.Fatalf("Get(delta) = (%q, %v)", got, err)
	}
	if got, err := dstVault.Get("alpha"); err != nil || got != "sk-alpha-1111" {
		t.Fatalf("Get(alpha) after write = (%q, %v)", got, err)
	}
}

func TestVaultReadOnlyDoesNotCreateKeyAndPreventsMixedKeyWrite(t *testing.T) {
	srcDir := t.TempDir()
	srcVault := NewVault(srcDir)
	if err := srcVault.SetMany(map[string]string{
		"alpha": "sk-alpha-1111",
		"beta":  "sk-beta-2222",
	}); err != nil {
		t.Fatalf("seed SetMany: %v", err)
	}
	rawSecrets, err := os.ReadFile(srcVault.SecretsPath())
	if err != nil {
		t.Fatalf("read secrets.json: %v", err)
	}

	// 仅复制 secrets.json 漏拷 secret.key：只读路径绝不能凭空新建垃圾 secret.key。
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dstDir, DefaultSecretsFile), rawSecrets, 0o600); err != nil {
		t.Fatalf("write secrets.json: %v", err)
	}
	dstVault := NewVault(dstDir)

	if _, err := dstVault.LoadAll(); !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("LoadAll without secret.key = %v, want ErrMasterKeyMissing", err)
	}
	if _, err := dstVault.Get("alpha"); !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("Get without secret.key = %v, want ErrMasterKeyMissing", err)
	}
	if _, statErr := os.Stat(dstVault.KeyPath()); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("read-only decryption must not create secret.key, stat err=%v", statErr)
	}

	// 部分覆盖写入（仍存在未覆盖的 beta）必须拒绝，防止双密钥混写永久损坏凭据库。
	if err := dstVault.Set("alpha", "sk-alpha-new"); !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("partial Set without secret.key = %v, want ErrMasterKeyMissing", err)
	}
	if err := dstVault.SetMany(map[string]string{"alpha": "sk-alpha-new"}); !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("partial SetMany without secret.key = %v, want ErrMasterKeyMissing", err)
	}
	if _, statErr := os.Stat(dstVault.KeyPath()); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("rejected partial write must not create secret.key, stat err=%v", statErr)
	}

	// 全量覆盖既有全部键时，允许安全重建新主密钥并保持库内密钥一致。
	if err := dstVault.SetMany(map[string]string{
		"alpha": "sk-alpha-rebuilt",
		"beta":  "sk-beta-rebuilt",
	}); err != nil {
		t.Fatalf("full overwrite SetMany without old key should succeed: %v", err)
	}
	rebuilt, err := dstVault.LoadAll()
	if err != nil || rebuilt["alpha"] != "sk-alpha-rebuilt" || rebuilt["beta"] != "sk-beta-rebuilt" {
		t.Fatalf("LoadAll after full rebuild = (%+v, %v)", rebuilt, err)
	}
}

func TestVaultErrorClassificationAndPermissions(t *testing.T) {
	// 1. 无效主密钥（空文件 / 非法 Base64 / 长度不符如旧版 DPAPI Blob / 超大文件 / 目录）均归类为 ErrMasterKeyInvalid。
	invalidKeyCases := []struct {
		name  string
		setup func(dir string) error
	}{
		{
			name: "stale_empty_key_not_deleted_on_read",
			setup: func(dir string) error {
				p := filepath.Join(dir, DefaultKeyFile)
				if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
					return err
				}
				oldTime := time.Now().Add(-10 * time.Second)
				return os.Chtimes(p, oldTime, oldTime)
			},
		},
		{
			name: "bad_base64",
			setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, DefaultKeyFile), []byte("not@@valid-base64\n"), 0o600)
			},
		},
		{
			name: "wrong_length_legacy_blob",
			setup: func(dir string) error {
				blob := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 128))
				return os.WriteFile(filepath.Join(dir, DefaultKeyFile), []byte(blob+"\n"), 0o600)
			},
		},
		{
			name: "oversized_key_file",
			setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, DefaultKeyFile), bytes.Repeat([]byte("A"), maxMasterKeyBytes+16), 0o600)
			},
		},
		{
			name: "key_path_is_dir",
			setup: func(dir string) error {
				return os.Mkdir(filepath.Join(dir, DefaultKeyFile), 0o700)
			},
		},
	}

	seedDir := t.TempDir()
	seedVault := NewVault(seedDir)
	if err := seedVault.Set("alpha", "sk-alpha-1111"); err != nil {
		t.Fatalf("seed Set: %v", err)
	}
	seedSecrets, err := os.ReadFile(seedVault.SecretsPath())
	if err != nil {
		t.Fatalf("read seed secrets: %v", err)
	}

	for _, tc := range invalidKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, DefaultSecretsFile), seedSecrets, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tc.setup(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := NewVault(dir).LoadAll(); !errors.Is(err, ErrMasterKeyInvalid) {
				t.Fatalf("LoadAll() = %v, want ErrMasterKeyInvalid", err)
			}
			if tc.name == "stale_empty_key_not_deleted_on_read" {
				if _, statErr := os.Stat(filepath.Join(dir, DefaultKeyFile)); statErr != nil {
					t.Fatalf("read-only path must not delete stale empty secret.key: %v", statErr)
				}
			}
		})
	}

	// 2. 损坏的 secrets.json（JSON 语法错误 / 缺少信封字段 / 不支持版本）均归类为 ErrVaultCorrupted。
	for _, raw := range []string{`{corrupt`, `{"data":{}}`, `{"schemaVersion":99,"data":{}}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, DefaultSecretsFile), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewVault(dir).LoadAll(); !errors.Is(err, ErrVaultCorrupted) {
			t.Fatalf("LoadAll(%q) = %v, want ErrVaultCorrupted", raw, err)
		}
	}

	// 3. 合法但未配对的 32 字节主密钥必须归类为 ErrDecryptFailed。
	mismatchDir := t.TempDir()
	otherKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7A}, masterKeySize)) + "\n"
	if err := os.WriteFile(filepath.Join(mismatchDir, DefaultKeyFile), []byte(otherKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mismatchDir, DefaultSecretsFile), seedSecrets, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewVault(mismatchDir).LoadAll(); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("LoadAll with mismatched key = %v, want ErrDecryptFailed", err)
	}

	// 4. 权限收紧与宽权限检测验证（Unix 0600 / 0644）。
	if runtime.GOOS != "windows" {
		permDir := t.TempDir()
		permVault := NewVault(permDir)
		if err := permVault.Set("alpha", "sk-perm-1111"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if storage.HasBroadPermissions(permVault.SecretsPath()) || storage.HasBroadPermissions(permVault.KeyPath()) {
			t.Fatal("newly created vault files must not report broad permissions")
		}
		if err := os.Chmod(permVault.SecretsPath(), 0o644); err != nil {
			t.Fatal(err)
		}
		if !storage.HasBroadPermissions(permVault.SecretsPath()) {
			t.Fatal("expected 0644 secrets.json to report broad permissions")
		}
		if err := permVault.Set("alpha", "sk-perm-2222"); err != nil {
			t.Fatalf("re-save Set: %v", err)
		}
		if storage.HasBroadPermissions(permVault.SecretsPath()) {
			t.Fatal("expected re-saved secrets.json permissions to be tightened back to 0600")
		}
	}

	// 5. 凭据文件与主密钥为符号链接/悬空符号链接时必须拒绝（不跟随、不篡改目标）。
	t.Run("reject_symlink_key_and_vault", func(t *testing.T) {
		symDir := t.TempDir()
		outsideKey := filepath.Join(symDir, "outside.key")
		outsideVault := filepath.Join(symDir, "outside.json")
		if err := os.WriteFile(outsideKey, []byte(otherKey), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outsideVault, seedSecrets, 0o644); err != nil {
			t.Fatal(err)
		}

		keyLinkDir := filepath.Join(symDir, "key-link")
		if err := os.MkdirAll(keyLinkDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keyLinkDir, DefaultSecretsFile), seedSecrets, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideKey, filepath.Join(keyLinkDir, DefaultKeyFile)); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink creation unavailable on Windows: %v", err)
			}
			t.Fatalf("Symlink secret.key: %v", err)
		}
		if _, err := NewVault(keyLinkDir).LoadAll(); !errors.Is(err, ErrMasterKeyInvalid) {
			t.Fatalf("LoadAll with symlink secret.key = %v, want ErrMasterKeyInvalid", err)
		}

		vaultLinkDir := filepath.Join(symDir, "vault-link")
		if err := os.MkdirAll(vaultLinkDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideVault, filepath.Join(vaultLinkDir, DefaultSecretsFile)); err != nil {
			t.Fatalf("Symlink secrets.json: %v", err)
		}
		if _, err := NewVault(vaultLinkDir).LoadAll(); !errors.Is(err, ErrVaultCorrupted) {
			t.Fatalf("LoadAll with symlink secrets.json = %v, want ErrVaultCorrupted", err)
		}

		danglingDir := filepath.Join(symDir, "dangling")
		if err := os.MkdirAll(danglingDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(symDir, "absent.json"), filepath.Join(danglingDir, DefaultSecretsFile)); err != nil {
			t.Fatalf("dangling Symlink secrets.json: %v", err)
		}
		if _, err := NewVault(danglingDir).LoadAll(); !errors.Is(err, ErrVaultCorrupted) {
			t.Fatalf("LoadAll with dangling secrets.json = %v, want ErrVaultCorrupted", err)
		}
		if runtime.GOOS != "windows" {
			for _, outside := range []string{outsideKey, outsideVault} {
				info, statErr := os.Stat(outside)
				if statErr != nil || info.Mode().Perm() != 0o644 {
					t.Fatalf("outside target %q mutated: err=%v mode=%v", outside, statErr, info)
				}
			}
		}

		vaultDirCase := filepath.Join(symDir, "vault-is-dir")
		if err := os.MkdirAll(filepath.Join(vaultDirCase, DefaultSecretsFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := NewVault(vaultDirCase).LoadAll(); !errors.Is(err, ErrVaultCorrupted) {
			t.Fatalf("LoadAll with directory secrets.json = %v, want ErrVaultCorrupted", err)
		}

		oversizedVaultDir := filepath.Join(symDir, "vault-oversized")
		if err := os.MkdirAll(oversizedVaultDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oversizedVaultDir, DefaultSecretsFile), bytes.Repeat([]byte("A"), maxVaultFileBytes+16), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewVault(oversizedVaultDir).LoadAll(); !errors.Is(err, ErrVaultCorrupted) {
			t.Fatalf("LoadAll with oversized secrets.json = %v, want ErrVaultCorrupted", err)
		}
	})

	// 6. 写路径遇到超过 5 秒的陈旧空 secret.key 时，可安全清理并重建有效主密钥。
	t.Run("stale_empty_key_rebuilt_on_write", func(t *testing.T) {
		staleDir := t.TempDir()
		keyPath := filepath.Join(staleDir, DefaultKeyFile)
		if err := os.WriteFile(keyPath, []byte(" \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-10 * time.Second)
		if err := os.Chtimes(keyPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
		v := NewVault(staleDir)
		if err := v.Set("alpha", "sk-rebuilt-9999"); err != nil {
			t.Fatalf("Set after stale empty key failed: %v", err)
		}
		if got, err := v.Get("alpha"); err != nil || got != "sk-rebuilt-9999" {
			t.Fatalf("Get(alpha) = (%q, %v)", got, err)
		}
	})
}

func BenchmarkSealerDecryptCached(b *testing.B) {
	sealer := New(b.TempDir())
	sealed, err := sealer.Encrypt("sk-benchmark-secret-key-1234567890")
	if err != nil {
		b.Fatalf("Encrypt failed: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sealer.Decrypt(sealed); err != nil {
			b.Fatalf("Decrypt failed: %v", err)
		}
	}
}

func BenchmarkVaultLoadAll(b *testing.B) {
	vault := NewVault(b.TempDir())
	seed := make(map[string]string, 12)
	for i := range 12 {
		seed[fmt.Sprintf("provider_%02d", i)] = fmt.Sprintf("sk-bench-key-%02d-abcdef1234567890", i)
	}
	if err := vault.SetMany(seed); err != nil {
		b.Fatalf("SetMany failed: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vault.LoadAll(); err != nil {
			b.Fatalf("LoadAll failed: %v", err)
		}
	}
}
