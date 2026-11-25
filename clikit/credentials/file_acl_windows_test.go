//go:build windows

package credentials

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsProtectedDACLAndBroadPermissionDetection(t *testing.T) {
	dir := t.TempDir()
	vault := NewVault(dir)

	if err := vault.Set("alpha", "sk-win-secret-1111"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if !volumeSupportsPersistentACLs(vault.SecretsPath()) {
		t.Skip("temporary volume does not support persistent NTFS ACLs")
	}

	if HasBroadPermissions(vault.KeyPath()) {
		t.Fatal("expected newly created secret.key not to have broad Windows ACLs")
	}
	if HasBroadPermissions(vault.SecretsPath()) {
		t.Fatal("expected newly created secrets.json not to have broad Windows ACLs")
	}

	// 向 secrets.json 添加 BUILTIN\Users 读权限，验证宽权限检测能够精确捕获。
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(Users): %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(vault.SecretsPath(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	oldDACL, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("sd.DACL: %v", err)
	}
	broadDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.FILE_GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_ALIAS,
			TrusteeValue: windows.TrusteeValueFromSID(usersSID),
		},
	}}, oldDACL)
	if err != nil {
		t.Fatalf("ACLFromEntries: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		vault.SecretsPath(),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, broadDACL, nil,
	); err != nil {
		t.Fatalf("SetNamedSecurityInfo(broad): %v", err)
	}
	if !HasBroadPermissions(vault.SecretsPath()) {
		t.Fatal("expected secrets.json with BUILTIN\\Users read ACE to report broad permissions")
	}

	// 模拟从只读介质复制后带有只读属性的目标文件，验证再次写入能够原子覆盖并重新收紧 ACL。
	if err := os.Chmod(vault.SecretsPath(), 0o400); err != nil {
		t.Fatalf("Chmod readonly: %v", err)
	}
	if err := vault.Set("alpha", "sk-win-secret-2222"); err != nil {
		t.Fatalf("re-save Set over readonly secrets.json failed: %v", err)
	}
	if HasBroadPermissions(vault.SecretsPath()) {
		t.Fatal("expected re-saved secrets.json ACL to be tightened back to trusted SIDs only")
	}
	if got, err := vault.Get("alpha"); err != nil || got != "sk-win-secret-2222" {
		t.Fatalf("Get(alpha) = (%q, %v)", got, err)
	}
}
