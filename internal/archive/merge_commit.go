package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	mergePlanFile       = "merge-plan.json"
	mergeSelectionFile  = "merge-selection.json"
	mergePlanFileLimit  = int64(64 << 20)
	mergeSelectionLimit = int64(2 << 20)
)

// MergeInspection is the restartable, non-mutating merge preview. The token
// is returned once and is never stored in plaintext.
type MergeInspection struct {
	Plan               MergePlan         `json:"plan"`
	Review             MergeReview       `json:"review"`
	Counts             ImportCounts      `json:"incoming_counts"`
	MediaBytes         int64             `json:"incoming_media_bytes"`
	Disk               DiskEstimate      `json:"disk"`
	Replacement        ReplacementImpact `json:"replacement"`
	MergeAllowed       bool              `json:"merge_allowed"`
	MergeBlockedReason *string           `json:"merge_blocked_reason"`
	Warnings           []string          `json:"warnings"`
	ExpiresAt          time.Time         `json:"expires_at"`
	StagingToken       string            `json:"staging_token,omitempty"`
}

// InspectMergeFileOffline creates a persistent merge plan while preventing a
// server from starting between the current snapshot and plan publication.
func (committer *Committer) InspectMergeFileOffline(ctx context.Context, archivePath string, subject Authorization) (result MergeInspection, resultErr error) {
	runtimeState, held, err := committer.acquireOfflineInspection(ctx)
	if err != nil {
		return MergeInspection{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	file, err := openArchiveInput(archivePath)
	if err != nil {
		return MergeInspection{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	inspection, err := committer.inspector.inspectWithinLocks(ctx, file, subject, runtimeState, held, true)
	if err != nil {
		return MergeInspection{}, err
	}
	result, err = committer.mergeInspectionWithinLocks(ctx, runtimeState, held, inspection, nil)
	if err != nil {
		err = errors.Join(err, committer.inspector.discardReadyWithinLocks(runtimeState, inspection.GenerationID, held))
	}
	return result, err
}

// InspectMergeArchive is the live-server merge planning path. It owns the
// import coordinator through staging and plan persistence but never enters
// maintenance or mutates active authority.
func (committer *Committer) InspectMergeArchive(ctx context.Context, source io.Reader, subject Authorization, activeDatabase *sqlitestore.Database) (result MergeInspection, resultErr error) {
	if committer == nil || source == nil || !subject.Valid() {
		return MergeInspection{}, domain.NewError(domain.CodeInvalidArgument, "merge inspection requires an archive and owner epoch", nil)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return MergeInspection{}, err
	}
	held, err := committer.inspector.acquireImportSet(ctx, runtimeState)
	if err != nil {
		return MergeInspection{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	inspection, err := committer.inspector.inspectWithinLocks(ctx, source, subject, runtimeState, held, true)
	if err != nil {
		return MergeInspection{}, err
	}
	result, err = committer.mergeInspectionWithinLocks(ctx, runtimeState, held, inspection, activeDatabase)
	if err != nil {
		err = errors.Join(err, committer.inspector.discardReadyWithinLocks(runtimeState, inspection.GenerationID, held))
	}
	return result, err
}

// DiscardMergeFileOffline abandons one reviewed merge plan while proving that
// no server can concurrently claim it. Active authority is never changed.
func (committer *Committer) DiscardMergeFileOffline(ctx context.Context, token string, subject Authorization) (resultErr error) {
	if committer == nil {
		return errors.New("import committer is required")
	}
	runtimeState, held, err := committer.acquireOfflineInspection(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return committer.discardMergeWithinLocks(ctx, runtimeState, held, token, subject)
}

// DiscardMerge abandons one unclaimed live-server merge plan. The exact
// staging credential and owner epoch are required; committing and terminal
// staging records are deliberately not cancellable through this path.
func (committer *Committer) DiscardMerge(ctx context.Context, token string, subject Authorization) (resultErr error) {
	if committer == nil {
		return errors.New("import committer is required")
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return err
	}
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return committer.discardMergeWithinLocks(ctx, runtimeState, held, token, subject)
}

func (committer *Committer) discardMergeWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, token string, subject Authorization) error {
	if runtimeState == nil || held == nil || !held.BelongsTo(runtimeState.Root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return runtimefs.ErrInsufficientLocks
	}
	state, _, stageRoot, err := committer.inspector.loadReadyHeld(ctx, runtimeState, token, subject)
	if err != nil {
		return err
	}
	if _, err := readMergePlan(stageRoot); err != nil {
		return importStagingInvalid("staged import is not a merge plan", err)
	}
	return committer.inspector.discardReadyWithinLocks(runtimeState, state.GenerationID, held)
}

// InspectReviewArchive creates one persistent comparison that can later be
// committed as either a same-owner merge or an exact replacement. Unlike the
// merge-only endpoint it intentionally allows a different incoming owner so a
// fresh instance can still restore its own portable backup.
func (committer *Committer) InspectReviewArchive(ctx context.Context, source io.Reader, subject Authorization, activeDatabase *sqlitestore.Database) (result MergeInspection, resultErr error) {
	if committer == nil || source == nil || !subject.Valid() {
		return MergeInspection{}, domain.NewError(domain.CodeInvalidArgument, "import review requires an archive and owner epoch", nil)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return MergeInspection{}, err
	}
	held, err := committer.inspector.acquireImportSet(ctx, runtimeState)
	if err != nil {
		return MergeInspection{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	inspection, err := committer.inspector.inspectWithinLocks(ctx, source, subject, runtimeState, held, true)
	if err != nil {
		return MergeInspection{}, err
	}
	result, err = committer.mergeInspectionWithinLocks(ctx, runtimeState, held, inspection, activeDatabase)
	if err != nil {
		err = errors.Join(err, committer.inspector.discardReadyWithinLocks(runtimeState, inspection.GenerationID, held))
	}
	return result, err
}

func (committer *Committer) mergeInspectionWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, inspection Inspection, activeDatabase *sqlitestore.Database) (result MergeInspection, resultErr error) {
	if runtimeState == nil || held == nil || !held.BelongsTo(runtimeState.Root) || !held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) {
		return MergeInspection{}, runtimefs.ErrInsufficientLocks
	}
	if err := runtimeState.RefreshCurrent(held); err != nil {
		return MergeInspection{}, err
	}
	closeActive := false
	if activeDatabase == nil {
		var err error
		activeDatabase, err = sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
		if err != nil {
			return MergeInspection{}, err
		}
		closeActive = true
	}
	if filepath.Clean(activeDatabase.Path()) != filepath.Clean(runtimeState.Current.DatabasePath) {
		if closeActive {
			_ = activeDatabase.Close()
		}
		return MergeInspection{}, errors.New("active merge database does not match CURRENT")
	}
	if closeActive {
		defer func() { resultErr = errors.Join(resultErr, activeDatabase.Close()) }()
	}
	local, err := ReadPortableAuthority(ctx, activeDatabase)
	if err != nil {
		return MergeInspection{}, err
	}
	stageRoot := filepath.Join(runtimeState.Root, "staging", "import", inspection.GenerationID)
	incomingDatabase, err := openMergeGenerationDatabase(ctx, runtimeState, stagingGeneration(stageRoot, inspection.GenerationID))
	if err != nil {
		return MergeInspection{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, incomingDatabase.Close()) }()
	incoming, err := ReadPortableAuthority(ctx, incomingDatabase)
	if err != nil {
		return MergeInspection{}, err
	}
	input := MergePlanInput{
		ArchiveSHA256: inspection.ArchiveSHA256, BaseGeneration: runtimeState.Current.ID,
		ExpiresAt: inspection.ExpiresAt, Local: local, Incoming: incoming,
	}
	plan, err := ComparePortableAuthorities(input)
	mergeAllowed := true
	if err != nil {
		return MergeInspection{}, err
	}
	review, err := buildMergeReview(plan, local, incoming)
	if err != nil {
		return MergeInspection{}, err
	}
	if err := writeMergePlan(stageRoot, plan); err != nil {
		return MergeInspection{}, err
	}
	var mergeBlockedReason *string
	if mergeUsernameCollision(local, incoming) != nil {
		reason := string(domain.CodeUsernameTaken)
		mergeAllowed, mergeBlockedReason = false, &reason
	}
	return MergeInspection{
		Plan: plan, Review: review, Counts: inspection.Counts, MediaBytes: inspection.MediaBytes,
		Disk: inspection.Disk, Replacement: inspection.Replacement, MergeAllowed: mergeAllowed,
		MergeBlockedReason: mergeBlockedReason, Warnings: append([]string{}, inspection.Warnings...),
		ExpiresAt: inspection.ExpiresAt, StagingToken: inspection.StagingToken,
	}, nil
}

// CommitReviewedReplacement consumes a reviewed token through the existing
// atomic replacement state machine after proving that the local authority is
// still exactly the version shown during review.
func (committer *Committer) CommitReviewedReplacement(ctx context.Context, token string, subject Authorization, planID string, host CommitHost) (result CommitResult, resultErr error) {
	prepare := committer.prepareReviewedReplacement(planID)
	if host == nil {
		runtimeState, held, err := committer.acquireOfflineInspection(ctx)
		if err != nil {
			return CommitResult{}, err
		}
		defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
		return committer.commitWithinLocks(ctx, runtimeState, held, token, subject, nil, prepare)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return CommitResult{}, err
	}
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return committer.commitWithinLocks(ctx, runtimeState, held, token, subject, host, prepare)
}

func (committer *Committer) prepareReviewedReplacement(planID string) commitPrepare {
	return func(ctx context.Context, runtimeState *runtimefs.Runtime, _ *runtimefs.LeaseSet, claim *StagingClaim, activeDatabase *sqlitestore.Database) error {
		plan, err := readMergePlan(claim.StageRoot)
		if err != nil {
			return importStagingInvalid("import review plan is missing or corrupt", err)
		}
		if planID == "" || plan.PlanID != planID {
			return importStagingInvalid("import review identity does not match", nil)
		}
		if runtimeState.Current.ID != plan.BaseGeneration {
			return staleMergePlan(plan, "active generation changed")
		}
		local, err := ReadPortableAuthority(ctx, activeDatabase)
		if err != nil {
			return err
		}
		localDigest, err := AuthorityDigest(local)
		if err != nil || localDigest != plan.BaseAuthoritySHA256 {
			return errors.Join(staleMergePlan(plan, "active authority changed"), err)
		}
		incomingDatabase, err := openMergeGenerationDatabase(ctx, runtimeState, claim.Generation)
		if err != nil {
			return err
		}
		incoming, readErr := ReadPortableAuthority(ctx, incomingDatabase)
		closeErr := incomingDatabase.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		incomingDigest, err := AuthorityDigest(incoming)
		if err != nil || incomingDigest != plan.IncomingAuthoritySHA256 {
			return errors.Join(importStagingInvalid("incoming review authority changed", nil), err)
		}
		return nil
	}
}

// ReadReviewMedia returns immutable image bytes from the exact local or incoming
// authority bound to a ready review token. Callers never receive filesystem
// paths and the token remains outside URLs.
func (committer *Committer) ReadReviewMedia(ctx context.Context, token string, subject Authorization, planID string, source MergeReviewSource, mediaID string) (result media.Asset, resultErr error) {
	if committer == nil || planID == "" || (source != MergeReviewLocal && source != MergeReviewIncoming) {
		return media.Asset{}, domain.NewError(domain.CodeInvalidArgument, "review media identity is invalid", nil)
	}
	parsedID, err := domain.ParseID(mediaID)
	if err != nil {
		return media.Asset{}, domain.NewError(domain.CodeInvalidArgument, "review media ID is invalid", nil)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return media.Asset{}, err
	}
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Shared})
	if err != nil {
		return media.Asset{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	state, _, stageRoot, err := committer.inspector.loadReadyHeld(ctx, runtimeState, token, subject)
	if err != nil {
		return media.Asset{}, err
	}
	plan, err := readMergePlan(stageRoot)
	if err != nil || plan.PlanID != planID {
		return media.Asset{}, importStagingInvalid("review media plan does not match", err)
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		return media.Asset{}, err
	}
	runtimeState.Current = current
	generation := current
	if source == MergeReviewIncoming {
		generation = stagingGeneration(stageRoot, state.GenerationID)
	} else if generation.ID != plan.BaseGeneration {
		return media.Asset{}, staleMergePlan(plan, "active generation changed")
	}
	reviewRuntime := &runtimefs.Runtime{Root: runtimeState.Root, Instance: runtimeState.Instance, Current: generation, Locks: runtimeState.Locks}
	database, err := sqlitestore.Open(ctx, reviewRuntime, sqlitestore.Options{})
	if err != nil {
		return media.Asset{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	store, err := media.NewStore(reviewRuntime, database, media.Options{})
	if err != nil {
		return media.Asset{}, err
	}
	return store.ReadOriginal(ctx, parsedID)
}

// CommitMerge consumes one merge token and explicit selection through the
// existing backup, atomic switch, readiness, and rollback state machine.
func (committer *Committer) CommitMerge(ctx context.Context, token string, subject Authorization, selection MergeSelection, host CommitHost) (result CommitResult, resultErr error) {
	prepare := committer.prepareMerge(selection)
	if host == nil {
		runtimeState, held, err := committer.acquireOfflineInspection(ctx)
		if err != nil {
			return CommitResult{}, err
		}
		defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
		return committer.commitWithinLocks(ctx, runtimeState, held, token, subject, nil, prepare)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return CommitResult{}, err
	}
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	return committer.commitWithinLocks(ctx, runtimeState, held, token, subject, host, prepare)
}

func (committer *Committer) prepareMerge(selection MergeSelection) commitPrepare {
	return func(ctx context.Context, runtimeState *runtimefs.Runtime, _ *runtimefs.LeaseSet, claim *StagingClaim, activeDatabase *sqlitestore.Database) (resultErr error) {
		plan, err := readMergePlan(claim.StageRoot)
		if err != nil {
			return importStagingInvalid("merge plan is missing or corrupt", err)
		}
		if runtimeState.Current.ID != plan.BaseGeneration {
			return staleMergePlan(plan, "active generation changed")
		}
		local, err := ReadPortableAuthority(ctx, activeDatabase)
		if err != nil {
			return err
		}
		localDigest, err := AuthorityDigest(local)
		if err != nil || localDigest != plan.BaseAuthoritySHA256 {
			return errors.Join(staleMergePlan(plan, "active authority changed"), err)
		}
		incomingDatabase, err := openMergeGenerationDatabase(ctx, runtimeState, claim.Generation)
		if err != nil {
			return err
		}
		incoming, readErr := ReadPortableAuthority(ctx, incomingDatabase)
		closeErr := incomingDatabase.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		incomingDigest, err := AuthorityDigest(incoming)
		if err != nil || incomingDigest != plan.IncomingAuthoritySHA256 {
			return errors.Join(importStagingInvalid("incoming merge authority changed", nil), err)
		}
		recomputed, err := ComparePortableAuthorities(MergePlanInput{
			ArchiveSHA256: claim.ArchiveSHA, BaseGeneration: runtimeState.Current.ID,
			ExpiresAt: claim.ExpiresAt, Local: local, Incoming: incoming,
		})
		if err != nil || !reflect.DeepEqual(recomputed, plan) {
			return errors.Join(importStagingInvalid("persisted merge plan no longer matches its authorities", nil), err)
		}
		resolved, err := ResolvePortableMerge(plan, selection, local, incoming, MergeResolveOptions{Clock: committer.clock})
		if err != nil {
			return err
		}
		digest, err := SelectionDigest(selection)
		if err != nil {
			return err
		}
		if err := writeMergeSelection(claim.StageRoot, selection, digest); err != nil {
			return err
		}
		nextRoot := filepath.Join(claim.StageRoot, "merge-next")
		if err := os.Mkdir(nextRoot, 0o700); err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(nextRoot)) }()
		nextGeneration, err := createStagingGeneration(nextRoot, claim.GenerationID)
		if err != nil {
			return err
		}
		if err := rebuildResolvedMergeGeneration(ctx, runtimeState, nextGeneration, resolved, runtimeState.Current.Root, claim.Generation.Root); err != nil {
			return err
		}
		incomingRoot := claim.Generation.Root + ".incoming"
		if err := os.Rename(claim.Generation.Root, incomingRoot); err != nil {
			return err
		}
		if err := os.Rename(nextGeneration.Root, claim.Generation.Root); err != nil {
			_ = os.Rename(incomingRoot, claim.Generation.Root)
			return err
		}
		if err := runtimefs.SyncDirectory(claim.StageRoot); err != nil {
			return err
		}
		if err := os.RemoveAll(incomingRoot); err != nil {
			return err
		}
		return runtimefs.SyncDirectory(claim.StageRoot)
	}
}

func openMergeGenerationDatabase(ctx context.Context, parent *runtimefs.Runtime, generation runtimefs.Generation) (*sqlitestore.Database, error) {
	return sqlitestore.Open(ctx, &runtimefs.Runtime{Root: parent.Root, Instance: parent.Instance, Current: generation, Locks: parent.Locks}, sqlitestore.Options{})
}

func staleMergePlan(plan MergePlan, reason string) error {
	return domain.NewError(domain.CodeImportMergePlanStale, "merge plan is stale; inspect the archive again", map[string]any{"plan_id": plan.PlanID, "reason": reason})
}

func writeMergePlan(stageRoot string, plan MergePlan) error {
	if err := ValidateMergePlan(plan); err != nil {
		return err
	}
	data, err := json.Marshal(plan)
	if err != nil || int64(len(data)) > mergePlanFileLimit {
		return errors.Join(errors.New("merge plan exceeds its storage bound"), err)
	}
	return runtimefs.WritePrivateFileAtomic(stageRoot, mergePlanFile, append(data, '\n'))
}

func readMergePlan(stageRoot string) (MergePlan, error) {
	data, err := readBoundedMergeJSON(filepath.Join(stageRoot, mergePlanFile), mergePlanFileLimit)
	if err != nil {
		return MergePlan{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan MergePlan
	if err := decoder.Decode(&plan); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return MergePlan{}, errors.New("merge plan JSON is invalid")
	}
	return plan, ValidateMergePlan(plan)
}

func writeMergeSelection(stageRoot string, selection MergeSelection, digest string) error {
	document := struct {
		SelectionSHA256 string         `json:"selection_sha256"`
		Selection       MergeSelection `json:"selection"`
	}{digest, selection}
	data, err := json.Marshal(document)
	if err != nil || int64(len(data)) > mergeSelectionLimit {
		return errors.Join(errors.New("merge selection exceeds its storage bound"), err)
	}
	return runtimefs.WritePrivateFileAtomic(stageRoot, mergeSelectionFile, append(data, '\n'))
}

func readBoundedMergeJSON(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 2 || info.Size() > limit {
		return nil, errors.Join(errors.New("merge authority file is unsafe"), err)
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) > limit || data[len(data)-1] != '\n' {
		return nil, errors.Join(errors.New("merge authority file bytes are invalid"), err)
	}
	return data[:len(data)-1], nil
}
