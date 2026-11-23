package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
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
