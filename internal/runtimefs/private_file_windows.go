//go:build windows

package runtimefs

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func protectPrivateFile(path string) error {
	sids, err := privateFileSIDs()
	if err != nil {
		return err
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids))
	for _, sid := range sids {
		pinner.Pin(sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build private file DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("protect private file DACL: %w", err)
	}
	return nil
}

// VerifyPrivateFilePermissions confirms that a Windows private file has a
// protected DACL whose only allow entries grant full access to the current
// process user, LocalSystem, and Builtin Administrators.
func VerifyPrivateFilePermissions(path string) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read private file DACL: %w", err)
	}
	if descriptor == nil || !descriptor.IsValid() {
		return errors.New("private file has no valid security descriptor")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read private file DACL control: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private file DACL inherits permissions")
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private file DACL entries: %w", err)
	}
	if dacl == nil || defaulted {
		return errors.New("private file DACL is empty or defaulted")
	}

	allowed, err := privateFileSIDSet()
	if err != nil {
		return err
	}
	if int(dacl.AceCount) != len(allowed) {
		return fmt.Errorf("private file DACL has %d entries, want %d", dacl.AceCount, len(allowed))
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read private file DACL entry %d: %w", index, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 {
			return fmt.Errorf("private file DACL entry %d is not a non-inheriting allow entry", index)
		}
		if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
			return fmt.Errorf("private file DACL entry %d does not grant full access", index)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return fmt.Errorf("private file DACL entry %d has an invalid SID", index)
		}
		name := sid.String()
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("private file DACL grants access to unexpected SID %s", name)
		}
		delete(allowed, name)
	}
	if len(allowed) != 0 {
		return errors.New("private file DACL is missing an allowed principal")
	}
	return nil
}

func privateFileSIDs() ([]*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current process user SID: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, fmt.Errorf("resolve LocalSystem SID: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, fmt.Errorf("resolve Builtin Administrators SID: %w", err)
	}

	unique := make(map[string]*windows.SID, 3)
	for _, sid := range []*windows.SID{user.User.Sid, system, administrators} {
		if sid == nil || !sid.IsValid() {
			return nil, errors.New("private file principal has an invalid SID")
		}
		unique[sid.String()] = sid
	}
	result := make([]*windows.SID, 0, len(unique))
	for _, sid := range unique {
		result = append(result, sid)
	}
	return result, nil
}

func privateFileSIDSet() (map[string]struct{}, error) {
	sids, err := privateFileSIDs()
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(sids))
	for _, sid := range sids {
		result[sid.String()] = struct{}{}
	}
	return result, nil
}
