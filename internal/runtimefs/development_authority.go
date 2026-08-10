package runtimefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DevelopmentAuthority is an unforgeable, handle-bound capability for the
// one unpublished development generation selected by Runtime.Current. It is
// deliberately constructed only after the instance root, generation tree,
// database descriptor, and complete destructive lock set agree.
type DevelopmentAuthority struct {
	runtimeRoot    string
	generationID   string
	root           *os.Root
	generationRoot *os.Root
	databasePath   string
	expected       DevelopmentGenerationAuthority
}

// BindDevelopmentAuthority proves that all supplied handles and the SQLite
// descriptor name address the exact identities pinned by expected. Callers
// retain ownership of the handles and lock set for the capability lifetime.
func BindDevelopmentAuthority(runtimeState *Runtime, root, generationRoot *os.Root, databasePath string, held *LeaseSet) (*DevelopmentAuthority, error) {
	if runtimeState == nil || !runtimeState.IsDevelopment() || root == nil || generationRoot == nil ||
		strings.TrimSpace(databasePath) == "" || runtimeState.Current.ID == "" ||
		held == nil || !held.BelongsTo(runtimeState.Root) || !holdsCompleteDevelopmentSet(held) {
		return nil, ErrInsufficientLocks
	}
	rootInfo, err := root.Stat(".")
	pathInfo, pathErr := os.Lstat(runtimeState.Root)
	if err != nil || pathErr != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(rootInfo, pathInfo) {
		return nil, errors.Join(ErrInsufficientLocks, errors.New("development root authority does not match runtime root"), err, pathErr)
	}
	markerInfo, markerErr := root.Lstat("instance.json")
	markerData, markerReadErr := root.ReadFile("instance.json")
	var instance Instance
	markerDecodeErr := decodeStrictInstance(markerData, &instance)
	if markerErr != nil || markerReadErr != nil || markerDecodeErr != nil || !markerInfo.Mode().IsRegular() ||
		markerInfo.Mode()&os.ModeSymlink != 0 || instance != runtimeState.Instance || instance.Environment != EnvironmentDevelopment {
		return nil, errors.Join(ErrInsufficientLocks, errors.New("development marker does not match runtime instance"), markerErr, markerReadErr, markerDecodeErr)
	}
	id := runtimeState.Current.ID
	generationInfo, generationErr := root.Lstat(filepath.Join("generations", id))
	databaseInfo, databaseErr := root.Lstat(filepath.Join("generations", id, "moment.db"))
	mediaInfo, mediaErr := root.Lstat(filepath.Join("generations", id, "media"))
	originalsInfo, originalsErr := root.Lstat(filepath.Join("generations", id, "media", "original"))
	if generationErr != nil || databaseErr != nil || mediaErr != nil || originalsErr != nil ||
		!generationInfo.IsDir() || !databaseInfo.Mode().IsRegular() || !mediaInfo.IsDir() || !originalsInfo.IsDir() ||
		generationInfo.Mode()&os.ModeSymlink != 0 || databaseInfo.Mode()&os.ModeSymlink != 0 ||
		mediaInfo.Mode()&os.ModeSymlink != 0 || originalsInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(ErrInsufficientLocks, errors.New("development generation tree is not a rooted ordinary tree"), generationErr, databaseErr, mediaErr, originalsErr)
	}
	expected := DevelopmentGenerationAuthority{
		ID: id, InstanceRoot: rootInfo, Root: generationInfo, Database: databaseInfo,
		Media: mediaInfo, Originals: originalsInfo,
	}
	capability := &DevelopmentAuthority{
		runtimeRoot: runtimeState.Root, generationID: id, root: root,
		generationRoot: generationRoot, databasePath: databasePath, expected: expected,
	}
	if err := capability.validate(runtimeState, held); err != nil {
		return nil, err
	}
	return capability, nil
}

// Validate re-proves the capability against a live runtime and lock set.
func (authority *DevelopmentAuthority) Validate(runtimeState *Runtime, held *LeaseSet) error {
	if authority == nil || runtimeState == nil || held == nil || runtimeState.Root != authority.runtimeRoot ||
		runtimeState.Current.ID != authority.generationID || !held.BelongsTo(runtimeState.Root) || !holdsCompleteDevelopmentSet(held) {
		return ErrInsufficientLocks
	}
	return authority.validate(runtimeState, held)
}

func (authority *DevelopmentAuthority) validate(_ *Runtime, _ *LeaseSet) error {
	rootInfo, err := authority.root.Stat(".")
	if err != nil || authority.expected.InstanceRoot == nil || !os.SameFile(rootInfo, authority.expected.InstanceRoot) {
		return errors.Join(ErrInsufficientLocks, errors.New("development root authority identity mismatch"), err)
	}
	generationInfo, err := authority.generationRoot.Stat(".")
	if err != nil || authority.expected.Root == nil || !generationInfo.IsDir() || !os.SameFile(generationInfo, authority.expected.Root) {
		return errors.Join(ErrInsufficientLocks, errors.New("development generation authority identity mismatch"), err)
	}
	for _, member := range []struct {
		rootPath string
		genPath  string
		expected os.FileInfo
		dir      bool
	}{
		{filepath.Join("generations", authority.generationID, "moment.db"), "moment.db", authority.expected.Database, false},
		{filepath.Join("generations", authority.generationID, "media"), "media", authority.expected.Media, true},
		{filepath.Join("generations", authority.generationID, "media", "original"), filepath.Join("media", "original"), authority.expected.Originals, true},
	} {
		rootMember, rootErr := authority.root.Lstat(member.rootPath)
		generationMember, generationErr := authority.generationRoot.Lstat(member.genPath)
		if rootErr != nil || generationErr != nil || member.expected == nil ||
			rootMember.Mode()&os.ModeSymlink != 0 || generationMember.Mode()&os.ModeSymlink != 0 ||
			rootMember.IsDir() != member.dir || generationMember.IsDir() != member.dir ||
			(!member.dir && (!rootMember.Mode().IsRegular() || !generationMember.Mode().IsRegular())) ||
			!os.SameFile(rootMember, member.expected) || !os.SameFile(generationMember, member.expected) {
			return errors.Join(ErrInsufficientLocks, errors.New("development generation member identity mismatch"), rootErr, generationErr)
		}
	}
	probe, err := os.OpenFile(authority.databasePath, os.O_RDWR, 0)
	if err != nil {
		return errors.Join(ErrInsufficientLocks, errors.New("development database descriptor is unavailable"), err)
	}
	probeInfo, statErr := probe.Stat()
	closeErr := probe.Close()
	if statErr != nil || closeErr != nil || authority.expected.Database == nil || !os.SameFile(probeInfo, authority.expected.Database) {
		return errors.Join(ErrInsufficientLocks, errors.New("development database descriptor identity mismatch"), statErr, closeErr)
	}
	return nil
}

func holdsCompleteDevelopmentSet(held *LeaseSet) bool {
	return held != nil && held.Holds(ImportLock, Exclusive) && held.Holds(ServeLock, Exclusive) &&
		held.Holds(MaintenanceLock, Exclusive) && held.Holds(MediaLock, Exclusive)
}

// RootAuthority returns the already-pinned instance authority. It must never
// be closed independently of the operation that owns this capability.
func (authority *DevelopmentAuthority) RootAuthority() *os.Root { return authority.root }

// GenerationRoot returns the already-pinned unpublished generation root.
func (authority *DevelopmentAuthority) GenerationRoot() *os.Root { return authority.generationRoot }

// DatabasePath returns the descriptor-backed SQLite name proven at bind time.
func (authority *DevelopmentAuthority) DatabasePath() string { return authority.databasePath }

// GenerationAuthority returns the exact identities required by rooted CURRENT
// publication and rollback.
func (authority *DevelopmentAuthority) GenerationAuthority() DevelopmentGenerationAuthority {
	return authority.expected
}
