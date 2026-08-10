//go:build windows

package runtimefs

import "testing"

func TestReplaceFileUsesWriteThroughPublication(t *testing.T) {
	original := callMoveFileExW
	t.Cleanup(func() { callMoveFileExW = original })
	var received uint32
	callMoveFileExW = func(_, _ *uint16, flags uint32) error {
		received = flags
		return nil
	}
	if err := replaceFile(`C:\data\.partial`, `C:\data\ready`); err != nil {
		t.Fatal(err)
	}
	want := uint32(movefileReplaceExisting | movefileWriteThrough)
	if received != want {
		t.Fatalf("MoveFileExW flags = %#x, want %#x", received, want)
	}
}

func TestPublishFileNoReplaceOmitsReplaceFlag(t *testing.T) {
	original := callMoveFileExW
	t.Cleanup(func() { callMoveFileExW = original })
	var received uint32
	callMoveFileExW = func(_, _ *uint16, flags uint32) error {
		received = flags
		return nil
	}
	if err := publishFileNoReplace(`C:\data\.partial`, `C:\data\ready`); err != nil {
		t.Fatal(err)
	}
	if received != movefileWriteThrough {
		t.Fatalf("MoveFileExW flags = %#x, want write-through without replace", received)
	}
}
