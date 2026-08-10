//go:build windows

package runtimefs

import (
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateFileDACLProtectsAndDiagnoses(t *testing.T) {
	root := t.TempDir()
	const name = "secret.json"
	if err := WritePrivateFileAtomic(root, name, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	path := root + string(os.PathSeparator) + name
	if err := VerifyPrivateFilePermissions(path); err != nil {
		t.Fatalf("protected file verification failed: %v", err)
	}
	if permissions, err := InspectPathPermissions(path); err != nil || !permissions.Read || !permissions.Write {
		t.Fatalf("protected file effective access = %#v, %v", permissions, err)
	}

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(everyone)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivateFilePermissions(path); err == nil {
		t.Fatal("Everyone-readable inherited DACL was accepted")
	}
	if permissions, err := InspectPathPermissions(path); err != nil || !permissions.Read || permissions.Write {
		t.Fatalf("read-only DACL effective access = %#v, %v", permissions, err)
	}
}
