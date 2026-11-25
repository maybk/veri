//go:build windows

package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	maxWindowsPathRunes = 32768
	// broadAccessMask 表示会向非受信任主体泄露凭据内容或允许篡改/接管权限的访问位。
	broadAccessMask = windows.FILE_READ_DATA |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.GENERIC_READ |
		windows.GENERIC_WRITE |
		windows.GENERIC_ALL
)

type trustedSIDEntry struct {
	sid         *windows.SID
	trusteeType windows.TRUSTEE_TYPE
}

// restrictFilePermissions 在支持持久 ACL 的 Windows 卷（如 NTFS）上将凭据文件收紧为 Protected DACL，
// 阻断父目录继承并仅授权当前用户、NT AUTHORITY\SYSTEM 与 BUILTIN\Administrators 完全控制。
func restrictFilePermissions(path string) error {
	if !volumeSupportsPersistentACLs(path) {
		return nil
	}
	trusted, err := trustedFileSIDs()
	if err != nil {
		return err
	}

	entries := make([]windows.EXPLICIT_ACCESS, 0, len(trusted))
	for _, item := range trusted {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  item.trusteeType,
				TrusteeValue: windows.TrusteeValueFromSID(item.sid),
			},
		})
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}

	// 仅更新受保护 DACL（不修改 OWNER/GROUP），避免非管理员普通账户因缺少 SeRestorePrivilege 失败。
	const secInfo = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, secInfo, nil, nil, dacl, nil)
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return nil
	}
	return err
}

// HasBroadPermissions 检查 Windows 凭据文件是否存在宽权限暴露（NULL DACL 或向非受信任主体授予读写/控制权限）。
func HasBroadPermissions(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if !volumeSupportsPersistentACLs(path) {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false
	}
	// Win32 中 NULL DACL（dacl == nil 且无错误）表示对所有人完全开放。
	if dacl == nil {
		return true
	}

	trusted, err := trustedFileSIDs()
	if err != nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if (ace.Header.AceFlags & windows.INHERIT_ONLY_ACE) != 0 {
			continue
		}
		if (ace.Mask & broadAccessMask) == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // G103: Win32 ACCESS_ALLOWED_ACE 尾部紧跟变长 SID 结构
		if !sid.IsValid() {
			continue
		}
		if !isTrustedSID(sid, trusted) {
			return true
		}
	}
	return false
}

func volumeSupportsPersistentACLs(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	utf16Path, err := windows.UTF16FromString(abs)
	if err != nil || len(utf16Path) == 0 {
		return false
	}
	bufLen := max(len(utf16Path)+1, windows.MAX_PATH+1)
	if bufLen > maxWindowsPathRunes {
		return false
	}
	volBuf := make([]uint16, bufLen)
	if err := windows.GetVolumePathName(&utf16Path[0], &volBuf[0], uint32(bufLen)); err != nil { //nolint:gosec // G115: bufLen 已限制 <= maxWindowsPathRunes
		return false
	}
	var fsFlags uint32
	if err := windows.GetVolumeInformation(&volBuf[0], nil, 0, nil, nil, &fsFlags, nil, 0); err != nil {
		return false
	}
	return (fsFlags & windows.FILE_PERSISTENT_ACLS) != 0
}

var (
	trustedSIDsMu    sync.RWMutex
	trustedSIDsCache []trustedSIDEntry
)

func trustedFileSIDs() ([]trustedSIDEntry, error) {
	trustedSIDsMu.RLock()
	if len(trustedSIDsCache) > 0 {
		cached := trustedSIDsCache
		trustedSIDsMu.RUnlock()
		return cached, nil
	}
	trustedSIDsMu.RUnlock()

	computed, err := computeTrustedFileSIDs()
	if err != nil {
		return nil, err
	}

	trustedSIDsMu.Lock()
	if len(trustedSIDsCache) == 0 {
		trustedSIDsCache = computed
	}
	cached := trustedSIDsCache
	trustedSIDsMu.Unlock()
	return cached, nil
}

func computeTrustedFileSIDs() ([]trustedSIDEntry, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sysSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}

	candidates := []trustedSIDEntry{
		{sid: tu.User.Sid, trusteeType: windows.TRUSTEE_IS_USER},
		{sid: sysSID, trusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP},
		{sid: admSID, trusteeType: windows.TRUSTEE_IS_ALIAS},
	}
	unique := make([]trustedSIDEntry, 0, len(candidates))
	for _, c := range candidates {
		if !isTrustedSID(c.sid, unique) {
			unique = append(unique, c)
		}
	}
	return unique, nil
}

func isTrustedSID(target *windows.SID, trusted []trustedSIDEntry) bool {
	for _, item := range trusted {
		if target.Equals(item.sid) {
			return true
		}
	}
	return false
}
