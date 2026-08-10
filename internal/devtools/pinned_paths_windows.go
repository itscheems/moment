//go:build windows

package devtools

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/moment/moment/internal/runtimefs"
	"golang.org/x/sys/windows"
)

type pinnedRuntimePaths struct {
	generationRoot *os.Root
	files          []*os.File
	databasePath   string
}

func openPinnedRuntimePaths(authority *os.Root, target *Target, expected runtimefs.DevelopmentGenerationAuthority) (*pinnedRuntimePaths, error) {
	if authority == nil || target == nil || expected.ID == "" {
		return nil, errors.New("pinned Windows runtime identity is required")
	}
	root := authority.Name()
	generationPath := filepath.Join(root, "generations", expected.ID)
	paths := []struct {
		path     string
		expected os.FileInfo
	}{
		{root, target.rootInfo},
		{filepath.Join(root, "generations"), target.pinned[filepath.Join(root, "generations")]},
		{generationPath, expected.Root},
		{filepath.Join(generationPath, "moment.db"), expected.Database},
		{filepath.Join(generationPath, "media"), expected.Media},
		{filepath.Join(generationPath, "media", "original"), expected.Originals},
		{filepath.Join(root, "cache"), target.pinned[filepath.Join(root, "cache")]},
		{filepath.Join(root, "cache", "previews"), target.pinned[filepath.Join(root, "cache", "previews")]},
	}
	result := &pinnedRuntimePaths{databasePath: filepath.Join(generationPath, "moment.db")}
	for _, member := range paths {
		file, err := openWindowsNoDelete(member.path)
		if err != nil {
			_ = result.Close()
			return nil, err
		}
		actual, actualErr := file.Stat()
		if member.expected == nil || actualErr != nil || !os.SameFile(member.expected, actual) {
			_ = file.Close()
			_ = result.Close()
			return nil, errors.Join(errors.New("Windows pinned handle identity mismatch"), actualErr)
		}
		result.files = append(result.files, file)
	}
	generationRoot, err := authority.OpenRoot(filepath.Join("generations", expected.ID))
	if err != nil {
		_ = result.Close()
		return nil, err
	}
	generationInfo, err := generationRoot.Stat(".")
	if err != nil || expected.Root == nil || !os.SameFile(generationInfo, expected.Root) {
		_ = generationRoot.Close()
		_ = result.Close()
		return nil, errors.Join(errors.New("Windows rooted generation identity mismatch"), err)
	}
	result.generationRoot = generationRoot
	return result, nil
}

func openWindowsNoDelete(path string) (*os.File, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return nil, err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.New("Windows pinned path must not be a reparse point")
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	access := uint32(windows.GENERIC_READ)
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	} else {
		access |= windows.GENERIC_WRITE
	}
	handle, err := windows.CreateFile(pointer, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func (paths *pinnedRuntimePaths) Close() error {
	if paths == nil {
		return nil
	}
	var closeErrors []error
	for index := len(paths.files) - 1; index >= 0; index-- {
		closeErrors = append(closeErrors, paths.files[index].Close())
	}
	if paths.generationRoot != nil {
		closeErrors = append(closeErrors, paths.generationRoot.Close())
	}
	return errors.Join(closeErrors...)
}
