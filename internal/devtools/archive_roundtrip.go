package devtools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type roundTripWorkspace struct {
	parent     *os.Root
	root       *os.Root
	leaf       string
	absolute   string
	rootInfo   os.FileInfo
	targetInfo os.FileInfo
}

func newRoundTripWorkspace(source *runtimefs.DevelopmentAuthority, sourceDataDir string) (*roundTripWorkspace, error) {
	if source == nil || source.RootAuthority() == nil {
		return nil, runtimefs.ErrInsufficientLocks
	}
	tmpInfo, err := source.RootAuthority().Lstat("tmp")
	if err != nil || !tmpInfo.IsDir() || tmpInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(errors.New("development roundtrip parent is unsafe"), err)
	}
	parent, err := source.RootAuthority().OpenRoot("tmp")
	if err != nil {
		return nil, err
	}
	openedParent, err := parent.Stat(".")
	if err != nil || !os.SameFile(tmpInfo, openedParent) {
		_ = parent.Close()
		return nil, errors.Join(errors.New("development roundtrip parent identity changed"), err)
	}
	var randomBytes [16]byte
	if _, err := io.ReadFull(rand.Reader, randomBytes[:]); err != nil {
		_ = parent.Close()
		return nil, err
	}
	leaf := "roundtrip-" + hex.EncodeToString(randomBytes[:])
	if err := parent.Mkdir(leaf, 0o700); err != nil {
		_ = parent.Close()
		return nil, err
	}
	root, err := parent.OpenRoot(leaf)
	if err != nil {
		_ = parent.RemoveAll(leaf)
		_ = parent.Close()
		return nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		_ = parent.RemoveAll(leaf)
		_ = parent.Close()
		return nil, err
	}
	if err := root.Mkdir("target", 0o700); err != nil {
		_ = root.Close()
		_ = parent.RemoveAll(leaf)
		_ = parent.Close()
		return nil, err
	}
	targetInfo, err := root.Lstat("target")
	if err != nil || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		_ = root.Close()
		_ = parent.RemoveAll(leaf)
		_ = parent.Close()
		return nil, errors.Join(errors.New("development roundtrip target is unsafe"), err)
	}
	return &roundTripWorkspace{
		parent: parent, root: root, leaf: leaf, absolute: filepath.Join(sourceDataDir, "tmp", leaf),
		rootInfo: rootInfo, targetInfo: targetInfo,
	}, nil
}

func (workspace *roundTripWorkspace) targetPath() string {
	return filepath.Join(workspace.absolute, "target")
}
func (workspace *roundTripWorkspace) archivePath() string {
	return filepath.Join(workspace.absolute, "archive.zip")
}

func (workspace *roundTripWorkspace) verify(requireArchive bool) error {
	rootInfo, err := workspace.parent.Lstat(workspace.leaf)
	openedInfo, openedErr := workspace.root.Stat(".")
	targetInfo, targetErr := workspace.root.Lstat("target")
	absoluteRoot, absoluteRootErr := os.Lstat(workspace.absolute)
	absoluteTarget, absoluteTargetErr := os.Lstat(workspace.targetPath())
	if err != nil || openedErr != nil || targetErr != nil || absoluteRootErr != nil || absoluteTargetErr != nil ||
		rootInfo.Mode()&os.ModeSymlink != 0 || targetInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(rootInfo, workspace.rootInfo) || !os.SameFile(openedInfo, workspace.rootInfo) ||
		!os.SameFile(absoluteRoot, workspace.rootInfo) || !os.SameFile(targetInfo, workspace.targetInfo) ||
		!os.SameFile(absoluteTarget, workspace.targetInfo) {
		return errors.Join(errors.New("development roundtrip workspace identity changed"), err, openedErr, targetErr, absoluteRootErr, absoluteTargetErr)
	}
	if requireArchive {
		rootedArchive, rootedErr := workspace.root.Lstat("archive.zip")
		absoluteArchive, absoluteErr := os.Lstat(workspace.archivePath())
		if rootedErr != nil || absoluteErr != nil || !rootedArchive.Mode().IsRegular() || rootedArchive.Mode()&os.ModeSymlink != 0 ||
			!os.SameFile(rootedArchive, absoluteArchive) {
			return errors.Join(errors.New("development roundtrip archive identity changed"), rootedErr, absoluteErr)
		}
	}
	return nil
}

func (workspace *roundTripWorkspace) cleanup(hooks *operationHooks) error {
	var hookErr error
	if hooks != nil && hooks.BeforeRoundTripCleanup != nil {
		hookErr = hooks.BeforeRoundTripCleanup(workspace.absolute)
	}
	closeRootErr := workspace.root.Close()
	leafInfo, leafErr := workspace.parent.Lstat(workspace.leaf)
	identityErr := error(nil)
	if leafErr != nil || leafInfo == nil || !os.SameFile(leafInfo, workspace.rootInfo) {
		identityErr = errors.Join(errors.New("development roundtrip workspace changed before cleanup"), leafErr)
	}
	removeErr := workspace.parent.RemoveAll(workspace.leaf)
	directory, openErr := workspace.parent.Open(".")
	var syncErr, closeDirectoryErr error
	if openErr == nil {
		syncErr = directory.Sync()
		closeDirectoryErr = directory.Close()
	}
	closeParentErr := workspace.parent.Close()
	return errors.Join(hookErr, closeRootErr, identityErr, removeErr, openErr, syncErr, closeDirectoryErr, closeParentErr)
}

func performArchiveRoundTrip(ctx context.Context, sourceRuntime *runtimefs.Runtime, sourceDatabase *sqlitestore.Database, sourceAuthority *runtimefs.DevelopmentAuthority, sourceHeld *runtimefs.LeaseSet, recipe Scenario, seed int64, hooks *operationHooks) (resultErr error) {
	if sourceAuthority == nil || sourceAuthority.Validate(sourceRuntime, sourceHeld) != nil {
		return runtimefs.ErrInsufficientLocks
	}
	workspace, err := newRoundTripWorkspace(sourceAuthority, sourceRuntime.Root)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, workspace.cleanup(hooks)) }()
	if hooks != nil && hooks.AfterRoundTripTarget != nil {
		if err := hooks.AfterRoundTripTarget(workspace.targetPath()); err != nil {
			return err
		}
	}
	if err := workspace.verify(false); err != nil {
		return err
	}
	archiveFile, err := workspace.root.OpenFile("archive.zip", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	exporter, err := archive.NewExporter(archive.Options{
		DataDir: sourceRuntime.Root, AppVersion: RecipeVersion,
		Clock: func() time.Time { return deterministicBase(recipe.Name, seed) },
	})
	if err == nil {
		_, err = exporter.ExportDevelopmentWithinLocks(ctx, archiveFile, sourceRuntime, sourceDatabase, sourceHeld, sourceAuthority)
	}
	syncErr := archiveFile.Sync()
	closeErr := archiveFile.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return errors.Join(err, syncErr, closeErr)
	}
	if err := workspace.verify(true); err != nil {
		return err
	}
	binding, err := workspace.bindArchive(ctx)
	if err != nil {
		return err
	}
	if err := initializeRoundTripTarget(ctx, workspace); err != nil {
		return fmt.Errorf("initialize isolated archive target: %w", err)
	}
	if err := workspace.verify(true); err != nil {
		return err
	}
	committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: workspace.targetPath(), AppVersion: RecipeVersion})
	if err != nil {
		return err
	}
	status, err := auth.ReadStatus(workspace.targetPath())
	if err != nil || !status.Initialized || status.SessionEpoch == 0 {
		return errors.Join(errors.New("roundtrip target owner authority is unavailable"), err)
	}
	plan, err := committer.PlanFileOffline(ctx, workspace.archivePath(), status.SessionEpoch)
	if err != nil {
		return fmt.Errorf("plan isolated archive import: %w", err)
	}
	if plan.ArchiveSHA256 != binding.SHA256 {
		return errors.New("roundtrip plan did not bind the exported archive digest")
	}
	if err := workspace.verify(true); err != nil {
		return err
	}
	if _, err := committer.CommitFileOffline(ctx, workspace.archivePath(), binding, status.SessionEpoch); err != nil {
		return fmt.Errorf("commit isolated archive import: %w", err)
	}
	if hooks != nil && hooks.AfterRoundTripCommit != nil {
		if err := hooks.AfterRoundTripCommit(); err != nil {
			return err
		}
	}
	if err := workspace.verify(true); err != nil {
		return err
	}
	if err := verifyRoundTripTarget(ctx, workspace); err != nil {
		return fmt.Errorf("verify isolated archive target: %w", err)
	}
	if sourceAuthority.Validate(sourceRuntime, sourceHeld) != nil {
		return errors.New("source development authority changed during archive roundtrip")
	}
	return nil
}

func initializeRoundTripTarget(ctx context.Context, workspace *roundTripWorkspace) (resultErr error) {
	bootstrap, err := OpenBootstrapTarget(workspace.targetPath())
	if err != nil {
		return err
	}
	authority, err := bootstrap.OpenAuthority()
	if err != nil {
		return err
	}
	if _, _, err := runtimefs.InitializeDevelopmentRoot(ctx, bootstrap.Root, authority); err != nil {
		_ = authority.Close()
		return err
	}
	if err := authority.Close(); err != nil {
		return err
	}
	target, err := OpenTarget(workspace.targetPath())
	if err != nil {
		return err
	}
	runtimeState, held, root, err := openCoordinatedTarget(ctx, target)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release(), root.Close()) }()
	expected := targetGenerationAuthority(target)
	pinned, err := openPinnedRuntimePaths(root, target, expected)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, pinned.Close()) }()
	capability, err := runtimefs.BindDevelopmentAuthority(runtimeState, root, pinned.generationRoot, pinned.databasePath, held)
	if err != nil {
		return err
	}
	credential, err := auth.InitializeDevelopmentWithinLocks(context.WithoutCancel(ctx), runtimeState, capability, held)
	if err != nil || !credential.Initialized || credential.SessionEpoch == 0 {
		return errors.Join(errors.New("initialize isolated roundtrip owner authority"), err)
	}
	return nil
}

func verifyRoundTripTarget(ctx context.Context, workspace *roundTripWorkspace) (resultErr error) {
	target, err := OpenTarget(workspace.targetPath())
	if err != nil {
		return err
	}
	runtimeState, held, root, err := openCoordinatedTarget(ctx, target)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release(), root.Close()) }()
	expected := targetGenerationAuthority(target)
	pinned, err := openPinnedRuntimePaths(root, target, expected)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, pinned.Close()) }()
	capability, err := runtimefs.BindDevelopmentAuthority(runtimeState, root, pinned.generationRoot, pinned.databasePath, held)
	if err != nil {
		return err
	}
	if current, currentErr := runtimeState.ResolveCurrentDevelopment(root, capability.GenerationAuthority().InstanceRoot); currentErr != nil {
		return fmt.Errorf("resolve restored rooted CURRENT: %w", currentErr)
	} else if current.ID != runtimeState.Current.ID {
		return errors.New("restored rooted CURRENT identity mismatch")
	}
	database, err := sqlitestore.OpenDevelopmentDiagnosticPinned(ctx, runtimeState, held, capability)
	if err != nil {
		return fmt.Errorf("open pinned restored database: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	diagnostic, err := doctor.New(doctor.Options{LockedSnapshot: &doctor.LockedSnapshot{
		Runtime: runtimeState, DiagnosticDatabase: database, Held: held, Authority: capability,
	}})
	if err != nil {
		return err
	}
	report, err := diagnostic.Run(ctx, doctor.ModeDeep)
	if err != nil {
		return err
	}
	if _, err := validateDoctorHandoff(report, "", false); err != nil {
		var ids []string
		for _, check := range report.Checks {
			if check.Status == doctor.StatusFail {
				ids = append(ids, check.ID)
			}
		}
		return fmt.Errorf("restored target doctor findings %v: %w", ids, err)
	}
	restored, err := workspace.root.OpenFile("restored.zip", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: runtimeState.Root, AppVersion: RecipeVersion})
	if err == nil {
		_, err = exporter.ExportDevelopmentDiagnosticWithinLocks(ctx, restored, runtimeState, database, held, capability)
	}
	syncErr := restored.Sync()
	closeErr := restored.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return errors.Join(err, syncErr, closeErr)
	}
	beforeDigest, err := portableSemanticDigestRooted(workspace.root, "archive.zip")
	if err != nil {
		return err
	}
	afterDigest, err := portableSemanticDigestRooted(workspace.root, "restored.zip")
	if err != nil {
		return err
	}
	if beforeDigest != afterDigest {
		return fmt.Errorf("archive roundtrip semantic digest drifted")
	}
	return nil
}

func (workspace *roundTripWorkspace) bindArchive(ctx context.Context) (archive.InputBinding, error) {
	rooted, err := workspace.root.Lstat("archive.zip")
	absolute, absoluteErr := os.Lstat(workspace.archivePath())
	if err != nil || absoluteErr != nil || !rooted.Mode().IsRegular() || rooted.Mode()&os.ModeSymlink != 0 || !os.SameFile(rooted, absolute) {
		return archive.InputBinding{}, errors.Join(errors.New("roundtrip archive is unsafe"), err, absoluteErr)
	}
	file, err := workspace.root.Open("archive.zip")
	if err != nil {
		return archive.InputBinding{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, roundTripContextReader{ctx: ctx, reader: file})
	opened, statErr := file.Stat()
	closeErr := file.Close()
	if copyErr != nil || statErr != nil || closeErr != nil || !os.SameFile(rooted, opened) || size != rooted.Size() {
		return archive.InputBinding{}, errors.Join(errors.New("roundtrip archive changed while binding"), copyErr, statErr, closeErr)
	}
	return archive.InputBinding{FileInfo: absolute, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

type roundTripContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader roundTripContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func portableSemanticDigestRooted(root *os.Root, name string) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", err
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		_ = file.Close()
		return "", err
	}
	digest, digestErr := portableSemanticDigestReader(reader)
	return digest, errors.Join(digestErr, file.Close())
}

func portableSemanticDigest(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	return portableSemanticDigestReader(reader)
}

func portableSemanticDigestReader(reader *zip.Reader) (string, error) {
	members := make([]*zip.File, 0, len(reader.File))
	for _, member := range reader.File {
		if strings.HasPrefix(member.Name, "data/") || strings.HasPrefix(member.Name, "media/") {
			members = append(members, member)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	digest := sha256.New()
	for _, member := range members {
		opened, err := member.Open()
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(digest, member.Name)
		_, _ = io.WriteString(digest, "\x00")
		_, copyErr := io.Copy(digest, opened)
		closeErr := opened.Close()
		if copyErr != nil || closeErr != nil {
			return "", errors.Join(copyErr, closeErr)
		}
		_, _ = io.WriteString(digest, "\x00")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
