package runtimefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Errors returned when a lock request does not satisfy the runtime contract.
var (
	ErrInvalidLock = errors.New("invalid runtime lock")
	ErrLockOrder   = errors.New("runtime locks must be acquired in the fixed order")
	ErrLockAbsent  = errors.New("persistent runtime lock is not initialized")
)

// LockName identifies one persistent advisory lock file.
type LockName string

const (
	// ImportLock serializes archive inspection and generation commits.
	ImportLock LockName = "import"
	// ServeLock is held exclusively for the lifetime of a server process.
	ServeLock LockName = "serve"
	// MaintenanceLock coordinates normal writes with maintenance operations.
	MaintenanceLock LockName = "maintenance"
	// MediaLock coordinates export readers with prune and import writers.
	MediaLock LockName = "media"
)

var lockOrder = map[LockName]int{
	ImportLock:      0,
	ServeLock:       1,
	MaintenanceLock: 2,
	MediaLock:       3,
}

var lockSequence = []LockName{ImportLock, ServeLock, MaintenanceLock, MediaLock}

// LockMode selects shared-reader or exclusive-writer ownership.
type LockMode uint8

const (
	// Shared permits other shared owners but excludes an exclusive owner.
	Shared LockMode = iota + 1
	// Exclusive excludes every other owner.
	Exclusive
)

// LockRequest is one member of an ordered acquisition set.
type LockRequest struct {
	Name LockName
	Mode LockMode
}

// Locker coordinates both goroutines and other processes for one data root.
// All waits honor context cancellation and deadlines.
type Locker struct {
	root             string
	gates            map[LockName]*localGate
	existingOnly     bool
	missing          map[LockName]bool
	permissionIssues []string
}

var localGates sync.Map

// NewLocker binds the four known lock files for a data directory.
func NewLocker(dataDir string) (*Locker, error) {
	root, err := ResolveDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	lockDirectory := filepath.Join(root, "locks")
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	if err := ensureLockFiles(lockDirectory); err != nil {
		return nil, err
	}
	return newLocker(root, false), nil
}

// OpenExistingLocker binds only already initialized persistent lock files.
// It is intended for strictly read-only diagnostics: it never creates a
// directory or file and never changes permissions.
func OpenExistingLocker(dataDir string) (*Locker, error) {
	root, err := ResolveDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	lockDirectory := filepath.Join(root, "locks")
	info, err := os.Lstat(lockDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(errors.New("runtime lock directory is missing or unsafe"), err)
	}
	permissionIssues := make([]string, 0, len(lockOrder)+1)
	if VerifyPrivateDirectoryPermissions(lockDirectory) != nil {
		permissionIssues = append(permissionIssues, "locks_directory")
	}
	missing := make(map[LockName]bool)
	for _, name := range lockSequence {
		path := filepath.Join(lockDirectory, string(name)+".lock")
		entry, inspectErr := os.Lstat(path)
		if errors.Is(inspectErr, os.ErrNotExist) && name != ImportLock {
			missing[name] = true
			continue
		}
		if inspectErr != nil {
			return nil, errors.Join(fmt.Errorf("runtime %s lock file is missing or unsafe", name), inspectErr)
		}
		if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("runtime %s lock file is missing or unsafe", name)
		}
		if VerifyWritablePrivateFilePermissions(path) != nil {
			permissionIssues = append(permissionIssues, string(name))
		}
	}
	locker := newLocker(root, true)
	locker.missing = missing
	locker.permissionIssues = permissionIssues
	return locker, nil
}

func ensureLockFiles(lockDirectory string) error {
	for _, name := range lockSequence {
		path := filepath.Join(lockDirectory, string(name)+".lock")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("initialize %s lock file: %w", name, err)
		}
		protectErr := file.Chmod(0o600)
		closeErr := file.Close()
		if protectErr != nil {
			return errors.Join(fmt.Errorf("protect %s lock file: %w", name, protectErr), closeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s lock file: %w", name, closeErr)
		}
	}
	return nil
}

func newLocker(root string, existingOnly bool) *Locker {
	lockDirectory := filepath.Join(root, "locks")
	gates := make(map[LockName]*localGate, len(lockOrder))
	for name := range lockOrder {
		key := filepath.Join(lockDirectory, string(name)+".lock")
		gate, _ := localGates.LoadOrStore(key, newLocalGate())
		gates[name] = gate.(*localGate)
	}
	return &Locker{root: root, gates: gates, existingOnly: existingOnly}
}

// Acquire obtains a single runtime lock.
func (l *Locker) Acquire(ctx context.Context, name LockName, mode LockMode) (*Lease, error) {
	if l != nil && l.missing[name] {
		return nil, fmt.Errorf("%w: %s", ErrLockAbsent, name)
	}
	leases, err := l.AcquireSet(ctx, LockRequest{Name: name, Mode: mode})
	if err != nil {
		return nil, err
	}
	return leases.leases[0], nil
}

// AcquireSet obtains locks in their mandatory global order and releases any
// partial acquisition if a later wait fails. Direct import includes ServeLock;
// API import omits it because the server already owns that lock.
func (l *Locker) AcquireSet(ctx context.Context, requests ...LockRequest) (*LeaseSet, error) {
	if err := validateLockRequests(requests); err != nil {
		return nil, err
	}
	set := &LeaseSet{root: l.root}
	for _, request := range requests {
		if l.missing[request.Name] {
			continue
		}
		lease, err := l.acquireOne(ctx, request)
		if err != nil {
			_ = set.Release()
			return nil, err
		}
		set.leases = append(set.leases, lease)
	}
	return set, nil
}

// HasPersistentLock reports whether one initialized lock file existed when a
// read-only locker snapshot was opened. It never probes ownership by filename.
func (l *Locker) HasPersistentLock(name LockName) bool {
	if l == nil {
		return false
	}
	_, valid := lockOrder[name]
	return valid && !l.missing[name]
}

// PermissionIssues returns controlled lock labels whose existing permissions
// are broader or less usable than the private runtime policy. It never changes
// modes or DACLs.
func (l *Locker) PermissionIssues() []string {
	if l == nil {
		return nil
	}
	return append([]string(nil), l.permissionIssues...)
}

// ExtendSet appends later-ranked locks to an existing acquisition without
// releasing the locks it already owns. This is used by import commit to claim
// a one-time token while holding import.lock, then enter the remaining lock
// phases without opening a replay window. A failed extension leaves the
// original set owned and releases only locks acquired by this call.
func (l *Locker) ExtendSet(ctx context.Context, set *LeaseSet, requests ...LockRequest) error {
	if l == nil || set == nil || set.root != l.root || set.released.Load() {
		return fmt.Errorf("%w: acquisition set is unavailable", ErrInvalidLock)
	}
	if err := validateLockRequests(requests); err != nil {
		return err
	}
	maximum := -1
	seen := make(map[LockName]struct{}, len(set.leases))
	for _, lease := range set.leases {
		if lease == nil || lease.released.Load() {
			continue
		}
		seen[lease.name] = struct{}{}
		if rank := lockOrder[lease.name]; rank > maximum {
			maximum = rank
		}
	}
	for _, request := range requests {
		if _, duplicate := seen[request.Name]; duplicate {
			return fmt.Errorf("%w: duplicate %s lock", ErrInvalidLock, request.Name)
		}
		if lockOrder[request.Name] <= maximum {
			return fmt.Errorf("%w: %s does not follow the held locks", ErrLockOrder, request.Name)
		}
		maximum = lockOrder[request.Name]
		seen[request.Name] = struct{}{}
	}
	var acquired []*Lease
	for _, request := range requests {
		lease, err := l.acquireOne(ctx, request)
		if err != nil {
			for index := len(acquired) - 1; index >= 0; index-- {
				_ = acquired[index].Release()
			}
			return err
		}
		acquired = append(acquired, lease)
	}
	set.leases = append(set.leases, acquired...)
	return nil
}

func (l *Locker) acquireOne(ctx context.Context, request LockRequest) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gate := l.gates[request.Name]
	localRelease, err := gate.acquire(ctx, request.Mode)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(l.root, "locks", string(request.Name)+".lock")
	var file *os.File
	var state platformLockState
	if l.existingOnly {
		file, state, err = acquireExistingFileLock(ctx, path, request.Mode)
	} else {
		file, state, err = acquireFileLock(ctx, path, request.Mode)
	}
	if err != nil {
		localRelease()
		return nil, fmt.Errorf("acquire %s lock: %w", request.Name, err)
	}
	return &Lease{name: request.Name, mode: request.Mode, file: file, state: state, localRelease: localRelease}, nil
}

func validateLockRequests(requests []LockRequest) error {
	if len(requests) == 0 {
		return fmt.Errorf("%w: acquisition set is empty", ErrInvalidLock)
	}
	previous := -1
	seen := make(map[LockName]struct{}, len(requests))
	for _, request := range requests {
		rank, valid := lockOrder[request.Name]
		if !valid || (request.Mode != Shared && request.Mode != Exclusive) {
			return fmt.Errorf("%w: name=%q mode=%d", ErrInvalidLock, request.Name, request.Mode)
		}
		if _, duplicate := seen[request.Name]; duplicate {
			return fmt.Errorf("%w: duplicate %s lock", ErrInvalidLock, request.Name)
		}
		if rank < previous {
			return fmt.Errorf("%w: %s follows a later-ranked lock", ErrLockOrder, request.Name)
		}
		previous = rank
		seen[request.Name] = struct{}{}
	}
	return nil
}

// Lease owns one OS advisory lock and its matching in-process gate.
type Lease struct {
	name         LockName
	mode         LockMode
	file         *os.File
	state        platformLockState
	localRelease func()
	once         sync.Once
	released     atomic.Bool
	err          error
}

// Holds reports whether this unreleased lease owns the requested lock mode.
func (l *Lease) Holds(name LockName, mode LockMode) bool {
	return l != nil && !l.released.Load() && l.name == name && l.mode == mode
}

// Release relinquishes ownership. It is safe to call more than once.
func (l *Lease) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.released.Store(true)
		unlockErr := unlockFile(l.file, &l.state)
		closeErr := l.file.Close()
		l.localRelease()
		l.err = errors.Join(unlockErr, closeErr)
	})
	return l.err
}

// LeaseSet owns a fixed-order lock group and releases it in reverse order.
type LeaseSet struct {
	leases   []*Lease
	root     string
	once     sync.Once
	err      error
	released atomic.Bool
}

// BelongsTo reports whether this live set was acquired from the exact runtime
// root. It prevents a same-named lock from another instance authorizing data
// mutation.
func (s *LeaseSet) BelongsTo(root string) bool {
	if s == nil || s.released.Load() {
		return false
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	return err == nil && absolute == s.root
}

// Holds reports whether the unreleased set contains the requested ownership.
func (s *LeaseSet) Holds(name LockName, mode LockMode) bool {
	if s == nil || s.released.Load() {
		return false
	}
	for _, lease := range s.leases {
		if lease.Holds(name, mode) {
			return true
		}
	}
	return false
}

// ReleaseLock relinquishes one member of a set while retaining the remaining
// ownership. ExtendSet still considers every retained lock, so releasing an
// earlier member cannot permit a later lock-order inversion.
func (s *LeaseSet) ReleaseLock(name LockName) error {
	if s == nil || s.released.Load() {
		return nil
	}
	var target *Lease
	for _, lease := range s.leases {
		if lease == nil || lease.released.Load() {
			continue
		}
		if lease.name == name {
			target = lease
		}
	}
	if target == nil {
		return fmt.Errorf("%w: set does not hold %s lock", ErrInvalidLock, name)
	}
	return target.Release()
}

// Release relinquishes the whole group in reverse acquisition order.
func (s *LeaseSet) Release() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.released.Store(true)
		var releaseErrors []error
		for index := len(s.leases) - 1; index >= 0; index-- {
			if err := s.leases[index].Release(); err != nil {
				releaseErrors = append(releaseErrors, err)
			}
		}
		s.err = errors.Join(releaseErrors...)
	})
	return s.err
}

func acquireFileLock(ctx context.Context, path string, mode LockMode) (*os.File, platformLockState, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, platformLockState{}, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, platformLockState{}, err
	}
	return waitForFileLock(ctx, file, mode)
}

func acquireExistingFileLock(ctx context.Context, path string, mode LockMode) (*os.File, platformLockState, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, platformLockState{}, err
	}
	return waitForFileLock(ctx, file, mode)
}

func waitForFileLock(ctx context.Context, file *os.File, mode LockMode) (*os.File, platformLockState, error) {
	state := platformLockState{}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, platformLockState{}, err
		}
		locked, err := tryLockFile(file, mode, &state)
		if err != nil {
			_ = file.Close()
			return nil, platformLockState{}, err
		}
		if locked {
			return file, state, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, platformLockState{}, ctx.Err()
		case <-timer.C:
		}
	}
}

type localGate struct {
	mu             sync.Mutex
	readers        int
	writer         bool
	waitingWriters int
	notify         chan struct{}
}

func newLocalGate() *localGate {
	return &localGate{notify: make(chan struct{})}
}

func (g *localGate) acquire(ctx context.Context, mode LockMode) (func(), error) {
	if mode == Shared {
		return g.acquireShared(ctx)
	}
	return g.acquireExclusive(ctx)
}

func (g *localGate) acquireShared(ctx context.Context) (func(), error) {
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if !g.writer && g.waitingWriters == 0 {
			g.readers++
			g.mu.Unlock()
			return g.releaseShared, nil
		}
		notify := g.notify
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

func (g *localGate) acquireExclusive(ctx context.Context) (func(), error) {
	g.mu.Lock()
	g.waitingWriters++
	g.mu.Unlock()
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.waitingWriters--
			g.broadcastLocked()
			g.mu.Unlock()
			return nil, err
		}
		if !g.writer && g.readers == 0 {
			g.waitingWriters--
			g.writer = true
			g.mu.Unlock()
			return g.releaseExclusive, nil
		}
		notify := g.notify
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.waitingWriters--
			g.broadcastLocked()
			g.mu.Unlock()
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

func (g *localGate) releaseShared() {
	g.mu.Lock()
	g.readers--
	g.broadcastLocked()
	g.mu.Unlock()
}

func (g *localGate) releaseExclusive() {
	g.mu.Lock()
	g.writer = false
	g.broadcastLocked()
	g.mu.Unlock()
}

func (g *localGate) broadcastLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}
