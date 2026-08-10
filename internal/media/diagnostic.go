package media

import (
	"context"
	"errors"
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const diagnosticPreviewVersionLimit = 32

// ErrDiagnosticIO distinguishes filesystem accessibility from authoritative
// media integrity without exposing a local path or underlying error text.
var ErrDiagnosticIO = errors.New("media diagnostic I/O failure")

// OriginalDiagnostic distinguishes path/availability failures from an
// expensive byte-level verification.
type OriginalDiagnostic struct {
	Present  bool `json:"present"`
	Verified bool `json:"verified"`
}

// DiagnoseOriginal validates one authoritative original without mutation.
// deep=false checks canonical location and file identity only; deep=true also
// runs the bounded checksum/header verifier shared with export/import.
func DiagnoseOriginal(ctx context.Context, generationRoot string, value Media, deep bool) (OriginalDiagnostic, error) {
	expected, ok := expectedStoragePath(value.SHA256, value.MIMEType)
	if !ok || value.StoragePath != expected {
		return OriginalDiagnostic{}, errors.New("media storage path is not canonical")
	}
	path := filepath.Join(generationRoot, filepath.FromSlash(value.StoragePath))
	relative, err := filepath.Rel(generationRoot, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return OriginalDiagnostic{}, errors.New("media storage path escapes the generation")
	}
	if err := diagnosePathTraversal(generationRoot, filepath.Dir(path)); err != nil {
		if errors.Is(err, ErrDiagnosticIO) {
			return OriginalDiagnostic{Present: true}, err
		}
		return OriginalDiagnostic{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrPermission) {
		return OriginalDiagnostic{}, errors.Join(ErrDiagnosticIO, err)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != value.ByteSize {
		return OriginalDiagnostic{}, errors.Join(errors.New("immutable original is missing or unsafe"), err)
	}
	result := OriginalDiagnostic{Present: true}
	permissions, permissionErr := runtimefs.InspectPathPermissions(path)
	if permissionErr != nil || !permissions.Read {
		return result, ErrDiagnosticIO
	}
	if !deep {
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return result, errors.Join(ErrDiagnosticIO, err)
	}
	verifyErr := VerifyPortableOriginal(ctx, file, value)
	closeErr := file.Close()
	if verifyErr != nil || closeErr != nil {
		return result, errors.Join(verifyErr, closeErr)
	}
	result.Verified = true
	return result, nil
}

func diagnosePathTraversal(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("media directory escapes the generation")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component != "." && component != "" {
			current = filepath.Join(current, component)
		}
		info, inspectErr := os.Lstat(current)
		if errors.Is(inspectErr, fs.ErrPermission) {
			return errors.Join(ErrDiagnosticIO, inspectErr)
		}
		if inspectErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(errors.New("media directory is missing or unsafe"), inspectErr)
		}
		permissions, permissionErr := runtimefs.InspectPathPermissions(current)
		if permissionErr != nil || !permissions.Read || !permissions.Traverse {
			return ErrDiagnosticIO
		}
	}
	return nil
}

// DiagnosePreview returns ready, stale, or missing for the current recipe. It
// only reads a bounded set of cache entries and image-header bytes; it never
// rebuilds or publishes derived data.
func DiagnosePreview(ctx context.Context, root, sha string) (string, error) {
	if len(sha) != 64 {
		return "stale", nil
	}
	limits, err := (Limits{}).normalized()
	if err != nil {
		return "stale", nil
	}
	currentPresent := false
	for _, candidate := range []struct {
		extension string
		format    string
	}{{extension: "png", format: "png"}, {extension: "jpg", format: "jpeg"}} {
		path := filepath.Join(root, "cache", "previews", RecipeVersion, sha[:2], sha+"."+candidate.extension)
		present, ready, inspectErr := diagnosePreviewFile(ctx, path, candidate.format, limits)
		if inspectErr != nil {
			return "stale", inspectErr
		}
		currentPresent = currentPresent || present
		if ready {
			return "ready", nil
		}
	}
	previewRoot := filepath.Join(root, "cache", "previews")
	directory, err := os.Open(previewRoot)
	if err != nil {
		return "missing", nil
	}
	versions, readErr := directory.ReadDir(diagnosticPreviewVersionLimit + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "stale", errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return "stale", closeErr
	}
	if len(versions) > diagnosticPreviewVersionLimit {
		return "stale", nil
	}
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return "stale", err
		}
		if !version.IsDir() {
			continue
		}
		if version.Name() == RecipeVersion {
			continue
		}
		for _, extension := range []string{"png", "jpg"} {
			if _, err := os.Lstat(filepath.Join(previewRoot, version.Name(), sha[:2], sha+"."+extension)); err == nil {
				return "stale", nil
			}
		}
	}
	if currentPresent {
		return "stale", nil
	}
	return "missing", nil
}

func diagnosePreviewFile(ctx context.Context, path, expectedFormat string, limits Limits) (bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > DefaultMaxBytes {
		return true, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return true, false, nil
	}
	config, decodedFormat, decodeErr := image.DecodeConfig(io.LimitReader(&contextReader{ctx: ctx, reader: file}, DefaultMaxBytes+1))
	closeErr := file.Close()
	if err := ctx.Err(); err != nil {
		return true, false, errors.Join(err, closeErr)
	}
	if decodeErr != nil || closeErr != nil || decodedFormat != expectedFormat || config.Width <= 0 || config.Height <= 0 ||
		config.Width > int(limits.PreviewMaxEdge) || config.Height > int(limits.PreviewMaxEdge) {
		return true, false, nil
	}
	return true, true, nil
}

// DiagnosticMedia converts storage metadata into the shared verifier value.
func DiagnosticMedia(id, sha, storagePath, originalName, mimeType string, byteSize int64, width, height uint32, createdAtMS int64) (Media, error) {
	parsed, err := domain.ParseID(id)
	if err != nil {
		return Media{}, err
	}
	return Media{
		ID: parsed, SHA256: sha, StoragePath: storagePath, OriginalName: originalName,
		MIMEType: mimeType, ByteSize: byteSize, Width: width, Height: height,
		CreatedAt: domain.TimeFromMillis(createdAtMS),
	}, nil
}
