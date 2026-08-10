//go:build linux

package devtools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moment/moment/internal/runtimefs"
)

type pinnedRuntimePaths struct {
	generationRoot *os.Root
	databaseFile   *os.File
	databasePath   string
}

func openPinnedRuntimePaths(authority *os.Root, target *Target, expected runtimefs.DevelopmentGenerationAuthority) (*pinnedRuntimePaths, error) {
	if authority == nil || target == nil || expected.ID == "" {
		return nil, errors.New("pinned Linux runtime identity is required")
	}
	rootInfo, err := authority.Stat(".")
	if err != nil || !os.SameFile(rootInfo, target.rootInfo) {
		return nil, errors.Join(errors.New("pinned Linux root identity mismatch"), err)
	}
	generationRoot, err := authority.OpenRoot(filepath.Join("generations", expected.ID))
	if err != nil {
		return nil, err
	}
	generationInfo, err := generationRoot.Stat(".")
	if err != nil || expected.Root == nil || !os.SameFile(generationInfo, expected.Root) {
		_ = generationRoot.Close()
		return nil, errors.Join(errors.New("pinned Linux generation identity mismatch"), err)
	}
	databaseFile, err := generationRoot.OpenFile("moment.db", os.O_RDWR, 0)
	if err != nil {
		_ = generationRoot.Close()
		return nil, err
	}
	databasePath := fmt.Sprintf("/proc/self/fd/%d", databaseFile.Fd())
	probe, aliasErr := os.OpenFile(databasePath, os.O_RDWR, 0)
	var aliasInfo os.FileInfo
	if aliasErr == nil {
		aliasInfo, aliasErr = probe.Stat()
		aliasErr = errors.Join(aliasErr, probe.Close())
	}
	pinnedInfo, pinnedErr := generationRoot.Stat("moment.db")
	if aliasErr != nil || pinnedErr != nil || expected.Database == nil || !os.SameFile(pinnedInfo, expected.Database) || !os.SameFile(aliasInfo, expected.Database) {
		_ = databaseFile.Close()
		_ = generationRoot.Close()
		return nil, fmt.Errorf("pinned Linux database descriptor is unavailable")
	}
	return &pinnedRuntimePaths{generationRoot: generationRoot, databaseFile: databaseFile, databasePath: databasePath}, nil
}

func (paths *pinnedRuntimePaths) Close() error {
	if paths == nil {
		return nil
	}
	return errors.Join(paths.databaseFile.Close(), paths.generationRoot.Close())
}
