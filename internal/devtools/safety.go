package devtools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const serveLockProbe = 150 * time.Millisecond

// Target pins the exact marked root, marker identity, instance identity, and
// CURRENT generation observed before a development operation takes locks.
type Target struct {
	Root       string
	Instance   runtimefs.Instance
	Current    runtimefs.Generation
	rootInfo   os.FileInfo
	markerInfo os.FileInfo
	rootVolume volumeIdentity
	pinned     map[string]os.FileInfo
}

// BootstrapTarget pins a strictly empty, explicit directory before seed or
// scenario initializes its one development marker. Reset never uses it.
type BootstrapTarget struct {
	Root       string
	rootInfo   os.FileInfo
	rootVolume volumeIdentity
}

// OpenBootstrapTarget accepts only an existing, exact, canonical, strictly
// empty directory. It never resolves an implicit platform default.
func OpenBootstrapTarget(dataDir string) (*BootstrapTarget, error) {
	return openBootstrapTarget(dataDir, true)
}

// OpenBootstrapRecoveryTarget accepts only a pinned root carrying a valid
// development marker. Runtimefs then validates the exact resumable allowlist.
func OpenBootstrapRecoveryTarget(dataDir string) (*BootstrapTarget, error) {
	return openBootstrapTarget(dataDir, false)
}

func openBootstrapTarget(dataDir string, requireEmpty bool) (*BootstrapTarget, error) {
	if strings.TrimSpace(dataDir) == "" || strings.ContainsRune(dataDir, 0) {
		return nil, unsafeTarget("an explicit --data-dir is required")
	}
	requested, err := filepath.Abs(filepath.Clean(dataDir))
	if err != nil || isVolumeRoot(requested) {
		return nil, errors.Join(unsafeTarget("the bootstrap directory cannot be a filesystem root"), err)
	}
	requestedInfo, err := os.Lstat(requested)
	if err != nil || !requestedInfo.IsDir() || requestedInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(requested, requestedInfo) {
		return nil, errors.Join(unsafeTarget("the bootstrap directory must be an existing ordinary directory"), err)
	}
	root, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return nil, errors.Join(unsafeTarget("the bootstrap directory cannot be canonicalized"), err)
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil || isVolumeRoot(root) {
		return nil, errors.Join(unsafeTarget("the canonical bootstrap directory is unsafe"), err)
	}
	if err := rejectSymlinkedPath(root); err != nil {
		return nil, errors.Join(unsafeTarget("the bootstrap path contains a symlink or reparse point"), err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(root, rootInfo) {
		return nil, errors.Join(unsafeTarget("the bootstrap root is unsafe"), err)
	}
	if !os.SameFile(requestedInfo, rootInfo) {
		return nil, unsafeTarget("the bootstrap root identity changed during canonicalization")
	}
	if isMountBoundary(root) {
		return nil, unsafeTarget("the bootstrap root must not be a mount or volume boundary")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		if home, homeErr = filepath.EvalSymlinks(home); homeErr == nil {
			if homeInfo, statErr := os.Lstat(home); statErr == nil && os.SameFile(rootInfo, homeInfo) {
				return nil, unsafeTarget("the user home directory cannot be a development data directory")
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || (requireEmpty && !bootstrapMembersAreRecoverableSkeleton(root, entries)) {
		return nil, errors.Join(unsafeTarget("development bootstrap requires a strictly empty directory"), err)
	}
	if !requireEmpty {
		instance, markerErr := runtimefs.ReadInstance(root)
		if markerErr != nil || instance.Environment != runtimefs.EnvironmentDevelopment {
			return nil, errors.Join(unmarkedTarget(), markerErr)
		}
	}
	volume, err := inspectVolumeIdentity(root)
	if err != nil {
		return nil, errors.Join(unsafeTarget("the bootstrap volume identity is unavailable"), err)
	}
	return &BootstrapTarget{Root: root, rootInfo: rootInfo, rootVolume: volume}, nil
}

func bootstrapMembersAreRecoverableSkeleton(root string, entries []os.DirEntry) bool {
	if len(entries) == 0 {
		return true
	}
	if len(entries) != 1 || entries[0].Name() != ".bootstrap.lock" {
		return false
	}
	info, err := os.Lstat(filepath.Join(root, entries[0].Name()))
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && !isReparsePoint(filepath.Join(root, entries[0].Name()), info)
}

// VerifyEmpty closes the initial inspection-to-initialization race as far as
// path APIs permit and refuses any concurrently introduced member.
func (target *BootstrapTarget) VerifyEmpty() error {
	if target == nil {
		return unsafeTarget("the bootstrap target is unavailable")
	}
	info, err := os.Lstat(target.Root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(target.Root, info) || !os.SameFile(target.rootInfo, info) {
		return errors.Join(unsafeTarget("the bootstrap root identity changed"), err)
	}
	volume, err := inspectVolumeIdentity(target.Root)
	if err != nil || volume != target.rootVolume || isMountBoundary(target.Root) {
		return errors.Join(unsafeTarget("the bootstrap root crossed a mount boundary"), err)
	}
	entries, err := os.ReadDir(target.Root)
	if err != nil || !bootstrapMembersAreRecoverableSkeleton(target.Root, entries) {
		return errors.Join(unsafeTarget("the bootstrap root is no longer empty"), err)
	}
	return nil
}

// VerifyIdentity rechecks a recovery root without requiring it to be empty;
// its members are validated handle-relatively by runtimefs.
func (target *BootstrapTarget) VerifyIdentity() error {
	if target == nil {
		return unsafeTarget("the bootstrap recovery target is unavailable")
	}
	info, err := os.Lstat(target.Root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(target.Root, info) || !os.SameFile(target.rootInfo, info) {
		return errors.Join(unsafeTarget("the bootstrap recovery root identity changed"), err)
	}
	volume, err := inspectVolumeIdentity(target.Root)
	if err != nil || volume != target.rootVolume || isMountBoundary(target.Root) {
		return errors.Join(unsafeTarget("the bootstrap recovery root crossed a mount boundary"), err)
	}
	return nil
}

// OpenAuthority binds bootstrap mutation to the identity pinned at inspection.
func (target *BootstrapTarget) OpenAuthority() (*os.Root, error) {
	if err := target.VerifyIdentity(); err != nil {
		return nil, err
	}
	authority, err := os.OpenRoot(target.Root)
	if err != nil {
		return nil, err
	}
	info, err := authority.Stat(".")
	if err != nil || !os.SameFile(info, target.rootInfo) {
		_ = authority.Close()
		return nil, errors.Join(unsafeTarget("the bootstrap root changed while opening its authority handle"), err)
	}
	return authority, nil
}

// OpenTarget fails closed for implicit, root, home, symlinked, unmarked,
// production, cross-volume, or structurally incomplete data directories.
func OpenTarget(dataDir string) (*Target, error) {
	if strings.TrimSpace(dataDir) == "" || strings.ContainsRune(dataDir, 0) {
		return nil, unsafeTarget("an explicit --data-dir is required")
	}
	requested, err := filepath.Abs(filepath.Clean(dataDir))
	if err != nil {
		return nil, unsafeTarget("the development data directory cannot be resolved")
	}
	if isVolumeRoot(requested) {
		return nil, unsafeTarget("a filesystem root cannot be a development data directory")
	}
	requestedInfo, err := os.Lstat(requested)
	if err != nil || requestedInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(requested, requestedInfo) {
		return nil, errors.Join(unsafeTarget("the development data directory itself must not be a symlink or reparse point"), err)
	}
	root, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return nil, errors.Join(unsafeTarget("the development data directory cannot be canonicalized"), err)
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil || isVolumeRoot(root) {
		return nil, errors.Join(unsafeTarget("the canonical development data directory is unsafe"), err)
	}
	if err := rejectSymlinkedPath(root); err != nil {
		return nil, errors.Join(unsafeTarget("the canonical development path contains a symlink or reparse point"), err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(unsafeTarget("the development data directory is missing or unsafe"), err)
	}
	if !os.SameFile(requestedInfo, rootInfo) {
		return nil, unsafeTarget("the development root identity changed during canonicalization")
	}
	if isMountBoundary(root) {
		return nil, unsafeTarget("the development data directory itself must not be a mount or volume boundary")
	}
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		home, homeErr = filepath.EvalSymlinks(home)
	}
	if homeErr == nil {
		homeInfo, statErr := os.Lstat(home)
		if statErr == nil && os.SameFile(rootInfo, homeInfo) {
			return nil, unsafeTarget("the user home directory cannot be a development data directory")
		}
	}
	markerPath := filepath.Join(root, "instance.json")
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(unmarkedTarget(), err)
	}
	instance, err := runtimefs.ReadInstance(root)
	if err != nil {
		return nil, errors.Join(unmarkedTarget(), err)
	}
	if instance.Environment != runtimefs.EnvironmentDevelopment {
		return nil, productionTarget()
	}
	current, err := (&runtimefs.Runtime{Root: root}).ResolveCurrent()
	if err != nil {
		return nil, errors.Join(unsafeTarget("CURRENT does not select one complete development generation"), err)
	}
	volume, err := inspectVolumeIdentity(root)
	if err != nil {
		return nil, errors.Join(unsafeTarget("the development root volume identity is unavailable"), err)
	}
	pinned := make(map[string]os.FileInfo)
	controlled := []string{
		filepath.Join(root, "CURRENT"), markerPath,
		filepath.Join(root, "generations"), filepath.Join(root, "locks"),
		filepath.Join(root, "staging"), filepath.Join(root, "staging", "import"),
		filepath.Join(root, "staging", "import-status"), filepath.Join(root, "tmp"),
		filepath.Join(root, "backups"), filepath.Join(root, "cache"), filepath.Join(root, "cache", "previews"),
		current.Root, current.DatabasePath, current.MediaRoot, current.OriginalsDir,
	}
	for _, path := range controlled {
		if err := rejectControlledPath(root, path); err != nil {
			return nil, errors.Join(unsafeTarget("the development instance contains an unsafe controlled path"), err)
		}
		info, inspectErr := os.Lstat(path)
		if inspectErr != nil || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(path, info) {
			return nil, errors.Join(unsafeTarget("the development instance contains an unsafe controlled path"), inspectErr)
		}
		pinned[path] = info
		memberVolume, inspectErr := inspectVolumeIdentity(path)
		if inspectErr != nil || memberVolume != volume {
			return nil, errors.Join(unsafeTarget("the development instance crosses a volume or mount boundary"), inspectErr)
		}
		if path != root && isMountBoundary(path) {
			return nil, unsafeTarget("a controlled development path is a mount or volume boundary")
		}
	}
	return &Target{
		Root: root, Instance: instance, Current: current, rootInfo: rootInfo,
		markerInfo: markerInfo, rootVolume: volume, pinned: pinned,
	}, nil
}

// Verify rechecks identity after lock acquisition and before every mutation.
func (target *Target) Verify() error {
	if target == nil {
		return unsafeTarget("the development target is unavailable")
	}
	rootInfo, err := os.Lstat(target.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(target.rootInfo, rootInfo) {
		return errors.Join(unsafeTarget("the development root identity changed during the operation"), err)
	}
	markerPath := filepath.Join(target.Root, "instance.json")
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(target.markerInfo, markerInfo) {
		return errors.Join(unsafeTarget("the development marker identity changed during the operation"), err)
	}
	instance, err := runtimefs.ReadInstance(target.Root)
	if err != nil || instance != target.Instance {
		return errors.Join(unsafeTarget("the marked development instance changed during the operation"), err)
	}
	current, err := (&runtimefs.Runtime{Root: target.Root}).ResolveCurrent()
	if err != nil || current.ID != target.Current.ID {
		return errors.Join(unsafeTarget("CURRENT changed during the development operation"), err)
	}
	volume, err := inspectVolumeIdentity(target.Root)
	if err != nil || volume != target.rootVolume {
		return errors.Join(unsafeTarget("the development root volume identity changed"), err)
	}
	for path, expected := range target.pinned {
		if err := rejectControlledPath(target.Root, path); err != nil {
			return errors.Join(unsafeTarget("a controlled development path became unsafe"), err)
		}
		actual, inspectErr := os.Lstat(path)
		if inspectErr != nil || actual.Mode()&os.ModeSymlink != 0 || isReparsePoint(path, actual) || !os.SameFile(expected, actual) {
			return errors.Join(unsafeTarget("a controlled development path identity changed"), inspectErr)
		}
		memberVolume, inspectErr := inspectVolumeIdentity(path)
		if inspectErr != nil || memberVolume != target.rootVolume || (path != target.Root && isMountBoundary(path)) {
			return errors.Join(unsafeTarget("a controlled development path crossed a mount or volume boundary"), inspectErr)
		}
	}
	return nil
}

func acquireOperationLocks(ctx context.Context, runtimeState *runtimefs.Runtime) (*runtimefs.LeaseSet, error) {
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return nil, err
	}
	lockContext, cancel := context.WithTimeout(ctx, serveLockProbe)
	err = runtimeState.Locks.ExtendSet(lockContext, held, runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive})
	cancel()
	if err == nil {
		err = runtimeState.Locks.ExtendSet(ctx, held,
			runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
		)
		if err == nil {
			return held, nil
		}
	}
	releaseErr := held.Release()
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), releaseErr)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.Join(domain.NewError(domain.CodeInstanceInUse, "Moment is serving this development instance", nil), releaseErr)
	}
	return nil, errors.Join(domain.NewError(domain.CodeInstanceInUse, "Moment is serving this development instance", nil), err, releaseErr)
}

func unsafeTarget(message string) error {
	return domain.NewError(domain.CodeInvalidArgument, message, map[string]any{"field": "data_dir"})
}

func unmarkedTarget() error {
	return domain.NewError(domain.CodeDataIntegrityFailed, "the selected data directory has no valid Moment development marker", map[string]any{"field": "data_dir"})
}

func productionTarget() error {
	return domain.NewError(domain.CodeInvalidArgument, "development commands refuse production instances", map[string]any{"field": "data_dir"})
}

func isVolumeRoot(path string) bool {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(filepath.Clean(path), volume)
	return rest == string(filepath.Separator)
}

func rejectSymlinkedPath(path string) error {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(filepath.Clean(path), volume)
	current := volume + string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(rest, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(current, info) {
			return fs.ErrInvalid
		}
	}
	return nil
}

func rejectControlledPath(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fs.ErrInvalid
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, inspectErr := os.Lstat(current)
		if inspectErr != nil || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(current, info) {
			return errors.Join(fs.ErrInvalid, inspectErr)
		}
	}
	return nil
}

func platformPathEqual(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func openCoordinatedTarget(ctx context.Context, target *Target) (*runtimefs.Runtime, *runtimefs.LeaseSet, *os.Root, error) {
	if err := target.Verify(); err != nil {
		return nil, nil, nil, err
	}
	authority, err := os.OpenRoot(target.Root)
	if err != nil {
		return nil, nil, nil, err
	}
	authorityInfo, err := authority.Stat(".")
	if err != nil || !os.SameFile(authorityInfo, target.rootInfo) {
		_ = authority.Close()
		return nil, nil, nil, errors.Join(unsafeTarget("the development root handle does not match the pinned target"), err)
	}
	locker, err := runtimefs.NewRootedLocker(target.Root, authority)
	if err != nil {
		_ = authority.Close()
		return nil, nil, nil, err
	}
	runtimeState := &runtimefs.Runtime{Root: target.Root, Instance: target.Instance, Current: target.Current, Locks: locker}
	held, err := acquireOperationLocks(ctx, runtimeState)
	if err != nil {
		_ = authority.Close()
		return nil, nil, nil, err
	}
	if err := target.Verify(); err != nil {
		_ = held.Release()
		_ = authority.Close()
		return nil, nil, nil, err
	}
	return runtimeState, held, authority, nil
}
