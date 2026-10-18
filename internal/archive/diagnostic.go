package archive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// StagingDiagnostic is a redacted, bounded view of persistent import state.
// It never contains a token, hash, archive identity, generation ID, or path.
type StagingDiagnostic struct {
	Total      int  `json:"total"`
	Ready      int  `json:"ready"`
	Expired    int  `json:"expired"`
	Committing int  `json:"committing"`
	Used       int  `json:"used"`
	Failed     int  `json:"failed"`
	Invalid    int  `json:"invalid"`
	Incomplete int  `json:"incomplete"`
	Bounded    bool `json:"bounded"`
}

// RecoveryDiagnostic summarizes crash/rollback journals without disclosing
// their local identities.
type RecoveryDiagnostic struct {
	Journals        int         `json:"journals"`
	Active          int         `json:"active"`
	NeedsAttention  int         `json:"needs_attention"`
	CurrentMismatch int         `json:"current_mismatch"`
	Invalid         int         `json:"invalid"`
	LatestPhase     CommitPhase `json:"latest_phase,omitempty"`
	Bounded         bool        `json:"bounded"`
}

const recoveryDiagnosticHardLimit = 4096

// DiagnosePersistentState reads only existing staging metadata and import
// journals. Parsing and phase validation reuse the archive package's authority
// contract rather than duplicating it in the doctor.
func DiagnosePersistentState(ctx context.Context, root, instanceID, currentGeneration string, now time.Time, limit int) (StagingDiagnostic, RecoveryDiagnostic, error, error) {
	if limit < 1 {
		err := errors.New("archive diagnostic limit must be positive")
		return StagingDiagnostic{}, RecoveryDiagnostic{}, err, err
	}
	staging, stagingErr := diagnoseStaging(ctx, root, instanceID, now.UTC(), limit)
	recovery, recoveryErr := diagnoseRecovery(ctx, root, currentGeneration, limit)
	return staging, recovery, stagingErr, recoveryErr
}

func diagnoseStaging(ctx context.Context, root, instanceID string, now time.Time, limit int) (StagingDiagnostic, error) {
	parent := filepath.Join(root, "staging", "import")
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return StagingDiagnostic{}, errors.Join(errors.New("import staging directory is missing or unsafe"), err)
	}
	entries, bounded, err := readDiagnosticDirectory(parent, limit)
	if err != nil {
		return StagingDiagnostic{}, err
	}
	if bounded {
		return StagingDiagnostic{Total: limit, Bounded: true}, nil
	}
	result := StagingDiagnostic{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Total++
		stageRoot := filepath.Join(parent, entry.Name())
		stageInfo, inspectErr := os.Lstat(stageRoot)
		if inspectErr != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "import-") {
			result.Invalid++
			continue
		}
		metadata, readErr := readStagingMetadata(stageRoot)
		if readErr != nil {
			result.Invalid++
			continue
		}
		hashesValid := metadata.State == stagingFailed || diagnosticHex(metadata.TokenHash) && diagnosticHex(metadata.ArchiveSHA)
		if metadata.InstanceID != instanceID || metadata.GenerationID != entry.Name() || !metadata.Authorization.Valid() || metadata.ExpiresAtMS <= 0 || !hashesValid {
			result.Invalid++
			continue
		}
		switch metadata.State {
		case stagingReady:
			if metadata.ExpiresAtMS <= now.UnixMilli() {
				result.Expired++
			} else {
				result.Ready++
			}
			complete, payloadErr := stagingPayloadComplete(ctx, root, stageRoot, metadata)
			if payloadErr != nil && ctx.Err() != nil {
				return result, ctx.Err()
			}
			if !complete {
				result.Incomplete++
			}
		case stagingCommitting:
			result.Committing++
			complete, payloadErr := stagingPayloadComplete(ctx, root, stageRoot, metadata)
			if payloadErr != nil && ctx.Err() != nil {
				return result, ctx.Err()
			}
			if !complete {
				result.Incomplete++
			}
		case stagingUsed:
			result.Used++
		case stagingFailed:
			result.Failed++
		default:
			result.Invalid++
		}
	}
	return result, nil
}

func diagnosticHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.IndexFunc(value, func(character rune) bool {
		return (character < '0' || character > '9') && (character < 'a' || character > 'f')
	}) == -1
}

func stagingPayloadComplete(ctx context.Context, runtimeRoot, stageRoot string, metadata stagingMetadata) (bool, error) {
	paths := []struct {
		name      string
		directory bool
	}{
		{name: "archive.zip"},
		{name: filepath.Join("generation", "moment.db")},
		{name: filepath.Join("generation", "media"), directory: true},
		{name: filepath.Join("generation", "media", "original"), directory: true},
	}
	for _, expected := range paths {
		info, err := os.Lstat(filepath.Join(stageRoot, expected.name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || expected.directory != info.IsDir() || !expected.directory && !info.Mode().IsRegular() {
			return false, nil
		}
	}
	archivePath := filepath.Join(stageRoot, "archive.zip")
	actualHash, err := fileSHA256(ctx, archivePath, defaultMaxArchiveBytes)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if actualHash != metadata.ArchiveSHA {
		return false, nil
	}
	if err := ValidateFileContext(ctx, archivePath); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	inspector, err := NewInspector(InspectOptions{})
	if err != nil {
		return false, err
	}
	document, err := inspector.preflight(ctx, archivePath)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	generation := stagingGeneration(stageRoot, metadata.GenerationID)
	if err := validatePortableGenerationTree(ctx, generation, archivePath); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	database, cleanup, err := OpenDiagnosticSnapshot(ctx, &runtimefs.Runtime{
		Root: runtimeRoot,
		Instance: runtimefs.Instance{
			ID: metadata.InstanceID,
		},
		Current: generation,
	}, defaultMaxExpandedBytes)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	validationErr := validateStagingAuthority(ctx, generation, archivePath, document, database)
	cleanupErr := cleanup()
	if validationErr != nil || cleanupErr != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, nil
}

func diagnoseRecovery(ctx context.Context, root, currentGeneration string, limit int) (RecoveryDiagnostic, error) {
	parent := filepath.Join(root, "staging", "import-status")
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return RecoveryDiagnostic{}, errors.Join(errors.New("import status directory is missing or unsafe"), err)
	}
	entries, hardBounded, err := readDiagnosticDirectory(parent, recoveryDiagnosticHardLimit)
	if err != nil {
		return RecoveryDiagnostic{}, err
	}
	if hardBounded {
		return RecoveryDiagnostic{Journals: recoveryDiagnosticHardLimit, Bounded: true}, errors.New("import recovery history exceeded its hard diagnostic bound")
	}
	result := RecoveryDiagnostic{Bounded: len(entries) > limit}
	hasLatest := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name := entry.Name()
		if name == commitLatestFile {
			hasLatest = true
			continue
		}
		if isCommitStatusTemporary(name) || isCommitLatestTemporary(name) {
			continue
		}
		result.Journals++
	}
	if result.Journals > limit {
		result.Journals = limit
	}
	latest, _, authorityErr := latestStatusFromEntries(root, parent, entries)
	if authorityErr != nil {
		if result.Journals == 0 && !hasLatest && domain.HasCode(authorityErr, domain.CodeImportStagingInvalid) {
			return result, nil
		}
		result.Invalid = 1
		return result, authorityErr
	}
	result.LatestPhase = latest.Phase
	if !terminalCommitPhase(latest.Phase) {
		result.Active = 1
	}
	if latest.Phase == CommitNeedsAttention {
		result.NeedsAttention = 1
	}
	if !commitPhaseMatchesCurrent(latest, currentGeneration) {
		result.CurrentMismatch = 1
	}
	return result, nil
}

func readDiagnosticDirectory(path string, limit int) ([]os.DirEntry, bool, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	entries, readErr := directory.ReadDir(limit + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, false, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	if len(entries) > limit {
		return nil, true, nil
	}
	return entries, false, nil
}

func commitPhaseMatchesCurrent(status CommitStatus, current string) bool {
	switch status.Phase {
	case CommitClaimed, CommitBackingUp, CommitFinalizing, CommitFailed, CommitRolledBack:
		return current == status.PreviousGeneration
	case CommitVerifying, CommitSucceeded:
		return current == status.NewGeneration
	case CommitSwitching, CommitRollingBack, CommitNeedsAttention:
		return current == status.PreviousGeneration || current == status.NewGeneration
	default:
		return false
	}
}
