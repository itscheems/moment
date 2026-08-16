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

// DiagnoseOriginalWithinRoot is the handle-relative form used by a locked
// development snapshot. No component is resolved again through the instance
// pathname after the caller has pinned its generation authority.
func DiagnoseOriginalWithinRoot(ctx context.Context, generationRoot *os.Root, value Media, deep bool) (OriginalDiagnostic, error) {
	if generationRoot == nil {
		return OriginalDiagnostic{}, ErrDiagnosticIO
	}
	expected, ok := expectedStoragePath(value.SHA256, value.MIMEType)
	if !ok || value.StoragePath != expected {
		return OriginalDiagnostic{}, errors.New("media storage path is not canonical")
	}
	relative := filepath.Clean(filepath.FromSlash(value.StoragePath))
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return OriginalDiagnostic{}, errors.New("media storage path escapes the generation")
	}
	if err := diagnoseRootedTraversal(generationRoot, filepath.Dir(relative)); err != nil {
		return OriginalDiagnostic{}, err
	}
	info, err := generationRoot.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != value.ByteSize {
		return OriginalDiagnostic{}, errors.Join(errors.New("immutable original is missing or unsafe"), err)
	}
	result := OriginalDiagnostic{Present: true}
	if !deep {
		return result, nil
	}
	file, err := generationRoot.Open(relative)
	if err != nil {
		return result, errors.Join(ErrDiagnosticIO, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return result, errors.Join(ErrDiagnosticIO, errors.New("immutable original changed while opening"), statErr)
	}
	verifyErr := VerifyPortableOriginal(ctx, file, value)
	closeErr := file.Close()
	if verifyErr != nil || closeErr != nil {
		return result, errors.Join(verifyErr, closeErr)
	}
	result.Verified = true
	return result, nil
}

func diagnoseRootedTraversal(root *os.Root, directory string) error {
	current := ""
	for _, component := range strings.Split(filepath.Clean(directory), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(errors.New("media directory is missing or unsafe"), err)
		}
	}
	return nil
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
	}{{extension: "png", format: "png"}, {extension: "jpg", format: "jpeg"}, {extension: "mp4", format: "mp4"}} {
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
		for _, extension := range []string{"png", "jpg", "mp4"} {
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

// DiagnosePreviewWithinRoot is the rooted counterpart used by locked
// development diagnostics.
func DiagnosePreviewWithinRoot(ctx context.Context, root *os.Root, sha string) (string, error) {
	if root == nil || len(sha) != 64 {
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
	}{{extension: "png", format: "png"}, {extension: "jpg", format: "jpeg"}, {extension: "mp4", format: "mp4"}} {
		path := filepath.Join("cache", "previews", RecipeVersion, sha[:2], sha+"."+candidate.extension)
		present, ready, inspectErr := diagnosePreviewFileWithinRoot(ctx, root, path, candidate.format, limits)
		if inspectErr != nil {
			return "stale", inspectErr
		}
		currentPresent = currentPresent || present
		if ready {
			return "ready", nil
		}
	}
	versions, readErr := fs.ReadDir(root.FS(), filepath.Join("cache", "previews"))
	if readErr != nil {
		return "missing", nil
	}
	if len(versions) > diagnosticPreviewVersionLimit {
		return "stale", nil
	}
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return "stale", err
		}
		if !version.IsDir() || version.Name() == RecipeVersion {
			continue
		}
		for _, extension := range []string{"png", "jpg", "mp4"} {
			if _, err := root.Lstat(filepath.Join("cache", "previews", version.Name(), sha[:2], sha+"."+extension)); err == nil {
				return "stale", nil
			}
		}
	}
	if currentPresent {
		return "stale", nil
	}
	return "missing", nil
}

func diagnosePreviewFileWithinRoot(ctx context.Context, root *os.Root, path, expectedFormat string, limits Limits) (bool, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limits.PreviewMaxBytes {
		return true, false, nil
	}
	file, err := root.Open(path)
	if err != nil {
		return true, false, nil
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return true, false, nil
	}
	if expectedFormat == "mp4" {
		header, readErr := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: file}, 64))
		closeErr := file.Close()
		if err := ctx.Err(); err != nil {
			return true, false, errors.Join(err, closeErr)
		}
		return true, readErr == nil && closeErr == nil && detectExtendedFormat(header) == formatMP4, errors.Join(readErr, closeErr)
	}
	config, decodedFormat, decodeErr := image.DecodeConfig(io.LimitReader(&contextReader{ctx: ctx, reader: file}, limits.PreviewMaxBytes+1))
	closeErr := file.Close()
	if err := ctx.Err(); err != nil {
		return true, false, err
	}
	if decodeErr != nil || closeErr != nil || decodedFormat != expectedFormat || config.Width < 1 || config.Height < 1 ||
		config.Width > int(limits.PreviewMaxEdge) || config.Height > int(limits.PreviewMaxEdge) {
		return true, false, nil
	}
	return true, true, nil
}

func diagnosePreviewFile(ctx context.Context, path, expectedFormat string, limits Limits) (bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limits.PreviewMaxBytes {
		return true, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return true, false, nil
	}
	if expectedFormat == "mp4" {
		header, readErr := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: file}, 64))
		closeErr := file.Close()
		if err := ctx.Err(); err != nil {
			return true, false, errors.Join(err, closeErr)
		}
		return true, readErr == nil && closeErr == nil && detectExtendedFormat(header) == formatMP4, errors.Join(readErr, closeErr)
	}
	config, decodedFormat, decodeErr := image.DecodeConfig(io.LimitReader(&contextReader{ctx: ctx, reader: file}, limits.PreviewMaxBytes+1))
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
