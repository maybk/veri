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
