package devtools

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/runtimefs"
)

const (
	timelineP95Limit        = 100 * time.Millisecond
	searchP95Limit          = 200 * time.Millisecond
	archiveP95Limit         = 250 * time.Millisecond
	streamRSSLimit          = uint64(768 << 20)
	performanceWarmup       = 5
	performanceRuns         = 31
	performanceStageTimeout = "30m"
)

type performanceMetric struct {
	Samples int     `json:"samples"`
	P95MS   float64 `json:"p95_ms"`
	LimitMS float64 `json:"limit_ms"`
}

type streamMetric struct {
	Bytes        int64  `json:"bytes"`
	PeakRSSBytes uint64 `json:"peak_rss_bytes"`
	LimitBytes   uint64 `json:"limit_bytes"`
	ElapsedMS    int64  `json:"elapsed_ms"`
}

type importValidationMetric struct {
	streamMetric
	TargetState                  string `json:"target_state"`
	TargetUnchanged              bool   `json:"target_unchanged"`
	ValidationBeforeActualImport bool   `json:"validation_before_actual_import"`
}

type binaryIdentity struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	SHA256    string `json:"sha256"`
}

type performanceEvidence struct {
	SchemaVersion  int    `json:"schema_version"`
	Qualified      bool   `json:"qualified"`
	ValidationOnly bool   `json:"validation_only"`
	Commit         string `json:"commit"`
	TotalElapsedMS int64  `json:"total_elapsed_ms"`
	Environment    struct {
		GOOS                      string `json:"goos"`
		GOARCH                    string `json:"goarch"`
		GoVersion                 string `json:"go_version"`
		GOMAXPROCS                int    `json:"gomaxprocs"`
		RSSSampleIntervalMS       int64  `json:"rss_sample_interval_ms"`
		DiskAvailableBeforeBytes  uint64 `json:"disk_available_before_bytes"`
		DiskAvailableAfterBytes   uint64 `json:"disk_available_after_bytes"`
		MinimumDiskAvailableBytes uint64 `json:"minimum_disk_available_bytes"`
		PeakDiskUsedBytes         uint64 `json:"peak_disk_used_bytes"`
	} `json:"environment"`
	Binary  binaryIdentity `json:"binary"`
	Fixture struct {
		ProbeScenario         string `json:"probe_scenario"`
		ProbeStageTimeout     string `json:"probe_stage_timeout"`
		ProbeTimeoutNoPartial bool   `json:"probe_timeout_no_partial"`
		ProbeArchiveRoundTrip bool   `json:"probe_archive_round_trip"`
		ProbeDeepDoctor       bool   `json:"probe_deep_doctor"`
		RecipeVersion         string `json:"recipe_version"`
		Entries               int64  `json:"entries"`
		Comments              int64  `json:"comments"`
		Media                 int64  `json:"media"`
		MediaBytes            int64  `json:"media_bytes"`
		PeakRSSBytes          uint64 `json:"peak_rss_bytes"`
		RSSLimitBytes         uint64 `json:"rss_limit_bytes"`
	} `json:"fixture"`
	Latency struct {
		Timeline performanceMetric `json:"timeline"`
		Search   performanceMetric `json:"search"`
		Archive  performanceMetric `json:"archive"`
	} `json:"latency"`
	Streaming struct {
		Export           streamMetric           `json:"export"`
		Import           streamMetric           `json:"import"`
		ImportValidation importValidationMetric `json:"import_validation"`
	} `json:"streaming"`
	ArchiveSHA256    string `json:"archive_sha256"`
	FixtureElapsedMS int64  `json:"fixture_elapsed_ms"`
}

func TestImportValidationEvidenceContract(t *testing.T) {
	evidence := performanceEvidence{}
	evidence.Streaming.ImportValidation = importValidationMetric{
		streamMetric:                 streamMetric{Bytes: 42, PeakRSSBytes: 7, LimitBytes: 9, ElapsedMS: 11},
		TargetState:                  "initialized-empty",
		TargetUnchanged:              true,
		ValidationBeforeActualImport: true,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	streaming := document["streaming"].(map[string]any)
	validation := streaming["import_validation"].(map[string]any)
	if validation["bytes"] != float64(42) || validation["peak_rss_bytes"] != float64(7) ||
		validation["limit_bytes"] != float64(9) || validation["elapsed_ms"] != float64(11) ||
		validation["target_state"] != "initialized-empty" || validation["target_unchanged"] != true ||
		validation["validation_before_actual_import"] != true {
		t.Fatalf("import validation evidence = %#v", validation)
	}
}

func localCommit(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 {
		t.Fatalf("invalid source commit %q", commit)
	}
	return commit
}

func releaseBinaryIdentity(t *testing.T, binary, expectedCommit string) binaryIdentity {
	t.Helper()
	metadata, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var envelope commandEnvelope[binaryIdentity]
	decodeCommandEnvelope(t, metadata, &envelope)
	if envelope.Data.Version == "" || envelope.Data.Version == "dev" || envelope.Data.Commit != expectedCommit || envelope.Data.BuildDate == "" || envelope.Data.BuildDate == "unknown" {
		t.Fatalf("release binary identity = %#v, expected commit %s", envelope.Data, expectedCommit)
	}
	binaryBuild, err := buildinfo.ReadFile(binary)
	if err != nil || binaryBuild.GoVersion == "" {
		t.Fatalf("read release binary build info: %v", err)
	}
	envelope.Data.GoVersion = binaryBuild.GoVersion
	binaryFile, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, binaryFile); err != nil {
		_ = binaryFile.Close()
		t.Fatal(err)
	}
	if err := binaryFile.Close(); err != nil {
		t.Fatal(err)
	}
	envelope.Data.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return envelope.Data
}

func decodeCommandEnvelope[T any](t *testing.T, output []byte, destination *commandEnvelope[T]) {
	t.Helper()
	if err := json.Unmarshal(output, destination); err != nil {
		t.Fatalf("decode release command JSON: %v", err)
	}
	if !destination.OK {
		t.Fatal("release command returned a non-success JSON envelope")
	}
}

type authorityTreeEntry struct {
	Path       string
	Type       string
	Mode       os.FileMode
	Size       int64
	SHA256     string
	LinkTarget string
}

func snapshotAuthorityTree(t *testing.T, root string) []authorityTreeEntry {
	t.Helper()
	entries := make([]authorityTreeEntry, 0, 32)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot := authorityTreeEntry{Path: filepath.ToSlash(relative), Mode: info.Mode()}
		switch {
		case info.Mode().IsDir():
			snapshot.Type = "directory"
		case info.Mode().IsRegular():
			snapshot.Type = "regular"
			snapshot.Size = info.Size()
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			digest := sha256.New()
			if _, err := io.Copy(digest, file); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			snapshot.SHA256 = hex.EncodeToString(digest.Sum(nil))
		case info.Mode()&os.ModeSymlink != 0:
			snapshot.Type = "symlink"
			snapshot.LinkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported authority tree member %s with mode %s", relative, info.Mode())
		}
		entries = append(entries, snapshot)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot authority tree %s: %v", root, err)
	}
	return entries
}

func assertDryRunTargetUnchanged(t *testing.T, before, after []authorityTreeEntry) {
	t.Helper()
	if !authorityTreesEqual(before, after) {
		t.Fatalf("dry-run changed initialized authority tree: before=%#v after=%#v", before, after)
	}
}

func authorityTreesEqual(before, after []authorityTreeEntry) bool {
	return reflect.DeepEqual(before, after)
}

func TestAuthorityTreeSnapshotIgnoresDirectorySizeAndDetectsRegularFileChanges(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "authority")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(child, "CURRENT")
	if err := os.WriteFile(name, []byte("generation-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotAuthorityTree(t, root)
	for _, entry := range before {
		if entry.Type == "directory" && (entry.Size != 0 || entry.SHA256 != "") {
			t.Fatalf("directory snapshot contains unstable metadata: %#v", entry)
		}
	}
	transient := filepath.Join(child, "transient")
	if err := os.WriteFile(transient, []byte("removed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(transient); err != nil {
		t.Fatal(err)
	}
	if afterDirectoryChurn := snapshotAuthorityTree(t, root); !authorityTreesEqual(before, afterDirectoryChurn) {
		t.Fatalf("directory inode size churn changed snapshot: before=%#v after=%#v", before, afterDirectoryChurn)
	}
	if err := os.WriteFile(name, []byte("generation-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := snapshotAuthorityTree(t, root)
	if authorityTreesEqual(before, after) {
		t.Fatal("regular authority file change was accepted")
	}
	if err := os.WriteFile(name, []byte("generation-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0o640); err != nil {
		t.Fatal(err)
	}
	if authorityTreesEqual(before, snapshotAuthorityTree(t, root)) {
		t.Fatal("regular authority mode change was accepted")
	}
	if err := os.WriteFile(filepath.Join(child, "added"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if authorityTreesEqual(before, snapshotAuthorityTree(t, root)) {
		t.Fatal("added authority member was accepted")
	}
	if err := os.Remove(filepath.Join(child, "added")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if authorityTreesEqual(before, snapshotAuthorityTree(t, root)) {
		t.Fatal("removed authority member was accepted")
	}
	typeChanged := append([]authorityTreeEntry(nil), before...)
	typeChanged[len(typeChanged)-1].Type = "symlink"
	if authorityTreesEqual(before, typeChanged) {
		t.Fatal("authority member type change was accepted")
	}
}

func validateRestoredDoctorRegistry(checks []doctor.Check) error {
	registry := doctor.Registry()
	if len(registry) != 21 {
		return fmt.Errorf("doctor schema v1 registry contains %d checks, want 21", len(registry))
	}
	if len(checks) != len(registry) {
		return fmt.Errorf("restored doctor returned %d checks, want %d", len(checks), len(registry))
	}
	seen := make(map[string]struct{}, len(checks))
	for index, check := range checks {
		if _, duplicate := seen[check.ID]; duplicate {
			return fmt.Errorf("restored doctor duplicated check %q", check.ID)
		}
		seen[check.ID] = struct{}{}
		if check.ID != registry[index] {
			return fmt.Errorf("restored doctor check %d = %q, want %q", index, check.ID, registry[index])
		}
	}
	return nil
}

func assertRestoredDoctorReport(t *testing.T, output []byte) {
	t.Helper()
	var envelope commandEnvelope[doctor.Report]
	decodeCommandEnvelope(t, output, &envelope)
	report := envelope.Data
	if report.SchemaVersion != doctor.SchemaVersion || report.Mode != doctor.ModeDeep ||
		report.FailureKind != doctor.FailureNone {
		t.Fatalf("restored doctor report identity = schema:%d mode:%q failure:%q checks:%d", report.SchemaVersion, report.Mode, report.FailureKind, len(report.Checks))
	}
	if err := validateRestoredDoctorRegistry(report.Checks); err != nil {
		t.Fatal(err)
	}
	registry := doctor.Registry()
	warnings := make([]doctor.Check, 0, 1)
	for _, check := range report.Checks {
		switch check.Status {
		case doctor.StatusPass:
			if check.Severity != doctor.SeverityInfo {
				t.Fatalf("restored doctor pass check %q severity = %q", check.ID, check.Severity)
			}
		case doctor.StatusWarning:
			warnings = append(warnings, check)
		default:
			t.Fatalf("restored doctor check %q status = %q", check.ID, check.Status)
		}
	}
	switch report.Status {
	case doctor.OverallHealthy:
		if report.Counts != (doctor.Counts{Pass: len(registry)}) || len(warnings) != 0 {
			t.Fatalf("healthy restored doctor counts=%#v warnings=%d", report.Counts, len(warnings))
		}
	case doctor.OverallWarning:
		if report.Counts != (doctor.Counts{Pass: len(registry) - 1, Warning: 1}) || len(warnings) != 1 {
			t.Fatalf("warning restored doctor counts=%#v warnings=%d", report.Counts, len(warnings))
		}
		warning := warnings[0]
		expectedEvidence := map[string]any{
			"total": float64(1), "ready": float64(0), "expired": float64(0), "used": float64(1),
			"failed": float64(0), "invalid": float64(0), "incomplete": float64(0), "bounded": false,
		}
		if warning.ID != doctor.CheckStaleImportStaging || warning.Severity != doctor.SeverityWarning ||
			warning.Summary != "import staging contains terminal or expired cleanup candidates" ||
			warning.Remediation != "allow normal retention cleanup or discard the stale staging explicitly" ||
			!reflect.DeepEqual(warning.Evidence, expectedEvidence) {
			t.Fatalf("restored doctor warning = %#v", warning)
		}
	default:
		t.Fatalf("restored doctor overall status = %q", report.Status)
	}
}

func TestRestoredDoctorReportContract(t *testing.T) {
	registry := doctor.Registry()
	checks := make([]doctor.Check, len(registry))
	for index, id := range registry {
		checks[index] = doctor.Check{ID: id, Status: doctor.StatusPass, Severity: doctor.SeverityInfo, Evidence: map[string]any{}}
	}
	encode := func(t *testing.T, report doctor.Report) []byte {
		t.Helper()
		output, err := json.Marshal(commandEnvelope[doctor.Report]{OK: true, Data: report})
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	t.Run("healthy", func(t *testing.T) {
		report := doctor.Report{
			SchemaVersion: doctor.SchemaVersion, Mode: doctor.ModeDeep, Status: doctor.OverallHealthy,
			Checks: checks, Counts: doctor.Counts{Pass: len(registry)},
		}
		assertRestoredDoctorReport(t, encode(t, report))
	})
	t.Run("retained import staging warning", func(t *testing.T) {
		warningChecks := append([]doctor.Check(nil), checks...)
		for index := range warningChecks {
			if warningChecks[index].ID == doctor.CheckStaleImportStaging {
				warningChecks[index] = doctor.Check{
					ID: doctor.CheckStaleImportStaging, Status: doctor.StatusWarning, Severity: doctor.SeverityWarning,
					Summary:     "import staging contains terminal or expired cleanup candidates",
					Remediation: "allow normal retention cleanup or discard the stale staging explicitly",
					Evidence: map[string]any{
						"total": 1, "ready": 0, "expired": 0, "used": 1,
						"failed": 0, "invalid": 0, "incomplete": 0, "bounded": false,
					},
				}
			}
		}
		report := doctor.Report{
			SchemaVersion: doctor.SchemaVersion, Mode: doctor.ModeDeep, Status: doctor.OverallWarning,
			Checks: warningChecks, Counts: doctor.Counts{Pass: len(registry) - 1, Warning: 1},
		}
		assertRestoredDoctorReport(t, encode(t, report))
	})
	t.Run("registry shape rejects drift", func(t *testing.T) {
		tests := map[string][]doctor.Check{
			"reordered": func() []doctor.Check {
				value := append([]doctor.Check(nil), checks...)
				value[0], value[1] = value[1], value[0]
				return value
			}(),
			"missing": append([]doctor.Check(nil), checks[:len(checks)-1]...),
			"extra":   append(append([]doctor.Check(nil), checks...), checks[0]),
		}
		for name, value := range tests {
			t.Run(name, func(t *testing.T) {
				if err := validateRestoredDoctorRegistry(value); err == nil {
					t.Fatal("doctor registry drift was accepted")
				}
			})
		}
	})
}

func probeReleaseBackupInvocation(t *testing.T, binary, workDir, sourceDir string) {
	t.Helper()
	timedOutArchive := filepath.Join(workDir, "fixture-probe-timeout.zip")
	timeoutCommand := exec.Command(binary, "backup", "export", "--data-dir", sourceDir, "--output", timedOutArchive,
		"--timeout", "1ns", "--no-input", "--json")
	timeoutOutput, timeoutErr := timeoutCommand.Output()
	exitError, ok := timeoutErr.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 9 {
		t.Fatalf("short backup timeout exit = %v, output=%s", timeoutErr, timeoutOutput)
	}
	var timeoutEnvelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(timeoutOutput, &timeoutEnvelope); err != nil || timeoutEnvelope.OK || timeoutEnvelope.Error.Code != "TIMEOUT" {
		t.Fatalf("short backup timeout envelope = %#v, err=%v", timeoutEnvelope, err)
	}
	if _, err := os.Stat(timedOutArchive); !os.IsNotExist(err) {
		t.Fatalf("short backup timeout left partial output: %v", err)
	}

	archivePath := filepath.Join(workDir, "fixture-probe.zip")
	exportOutput, err := exec.Command(binary, "backup", "export", "--data-dir", sourceDir, "--output", archivePath,
		"--timeout", performanceStageTimeout, "--no-input", "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup export probe: %v; stdout=%s", err, exportOutput)
	}
	var exported commandEnvelope[archive.Summary]
	decodeCommandEnvelope(t, exportOutput, &exported)
	archiveInfo, err := os.Stat(archivePath)
	if err != nil || archiveInfo.Size() == 0 || exported.Data.ArchiveBytes != archiveInfo.Size() {
		t.Fatalf("fixture backup export probe summary=%#v info=%v err=%v", exported.Data, archiveInfo, err)
	}

	validationDir := filepath.Join(workDir, "fixture-probe-import-validation")
	initializeOutput, err := exec.Command(binary, "owner", "init", "--data-dir", validationDir, "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup validation init probe: %v", err)
	}
	var initialized commandEnvelope[struct {
		Initialized bool `json:"initialized"`
	}]
	decodeCommandEnvelope(t, initializeOutput, &initialized)
	if !initialized.Data.Initialized {
		t.Fatal("fixture backup validation probe was not initialized")
	}
	validationBefore := snapshotAuthorityTree(t, validationDir)
	dryRunOutput, err := exec.Command(binary, "backup", "import", archivePath, "--data-dir", validationDir,
		"--timeout", performanceStageTimeout, "--dry-run", "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup dry-run probe: %v; stdout=%s", err, dryRunOutput)
	}
	var inspected commandEnvelope[archive.ImportPlan]
	decodeCommandEnvelope(t, dryRunOutput, &inspected)
	if inspected.Data.ArchiveSHA256 != exported.Data.ArchiveSHA256 {
		t.Fatalf("fixture backup dry-run hash = %q, want %q", inspected.Data.ArchiveSHA256, exported.Data.ArchiveSHA256)
	}
	assertDryRunTargetUnchanged(t, validationBefore, snapshotAuthorityTree(t, validationDir))

	restoreDir := filepath.Join(workDir, "fixture-probe-restore")
	initializeOutput, err = exec.Command(binary, "owner", "init", "--data-dir", restoreDir, "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup restore init probe: %v", err)
	}
	decodeCommandEnvelope(t, initializeOutput, &initialized)
	if !initialized.Data.Initialized {
		t.Fatal("fixture backup restore probe was not initialized")
	}
	importOutput, err := exec.Command(binary, "backup", "import", archivePath, "--data-dir", restoreDir,
		"--timeout", performanceStageTimeout, "--yes", "--no-input", "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup import probe: %v; stdout=%s", err, importOutput)
	}
	var imported commandEnvelope[archive.CommitResult]
	decodeCommandEnvelope(t, importOutput, &imported)
	if imported.Data.Status.Phase != archive.CommitSucceeded {
		t.Fatalf("fixture backup import probe phase = %q", imported.Data.Status.Phase)
	}
	doctorOutput, err := exec.Command(binary, "doctor", "--deep", "--data-dir", restoreDir,
		"--timeout", performanceStageTimeout, "--json").Output()
	if err != nil {
		t.Fatalf("fixture backup deep doctor probe: %v; stdout=%s", err, doctorOutput)
	}
	assertRestoredDoctorReport(t, doctorOutput)
}

func runMeasuredReleaseCommand(t *testing.T, binary, diskPath string, arguments ...string) ([]byte, uint64, uint64, time.Duration) {
	t.Helper()
	command := exec.Command(binary, arguments...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	type samples struct {
		peakRSS     uint64
		minimumDisk uint64
	}
	sampleResult := make(chan samples, 1)
	go func() {
		var peak uint64
		minimumDisk, _ := runtimefs.AvailableBytes(diskPath)
		iterations := 0
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if current := processRSSBytes(command.Process.Pid); current > peak {
				peak = current
			}
			select {
			case <-done:
				if current := processRSSBytes(command.Process.Pid); current > peak {
					peak = current
				}
				if available, sampleErr := runtimefs.AvailableBytes(diskPath); sampleErr == nil && (minimumDisk == 0 || available < minimumDisk) {
					minimumDisk = available
				}
				sampleResult <- samples{peakRSS: peak, minimumDisk: minimumDisk}
				return
			case <-ticker.C:
				iterations++
				if iterations%40 == 0 {
					if available, sampleErr := runtimefs.AvailableBytes(diskPath); sampleErr == nil && (minimumDisk == 0 || available < minimumDisk) {
						minimumDisk = available
					}
				}
			}
		}
	}()
	err := command.Wait()
	elapsed := time.Since(started)
	close(done)
	sampled := <-sampleResult
	if err != nil {
		stdoutMessage := strings.TrimSpace(stdout.String())
		if len(stdoutMessage) > 4096 {
			stdoutMessage = stdoutMessage[len(stdoutMessage)-4096:]
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4096 {
			message = message[len(message)-4096:]
		}
		t.Fatalf("release command %q failed after %s: %v; stdout=%s; stderr=%s", arguments[:3], elapsed, err, stdoutMessage, message)
	}
	if sampled.peakRSS == 0 {
		t.Fatalf("release command %q completed without an RSS sample", arguments[:3])
	}
	if sampled.minimumDisk == 0 {
		t.Fatalf("release command %q completed without a disk sample", arguments[:3])
	}
	return stdout.Bytes(), sampled.peakRSS, sampled.minimumDisk, elapsed
}

func processRSSBytes(processID int) uint64 {
	if runtime.GOOS == "linux" {
		contents, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(processID), "status"))
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
				kibibytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
				if parseErr == nil {
					return kibibytes * 1024
				}
			}
		}
		return 0
	}
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(processID)).Output()
	if err != nil {
		return 0
	}
	kibibytes, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return kibibytes * 1024
}

type commandEnvelope[T any] struct {
	OK   bool `json:"ok"`
	Data T    `json:"data"`
}

func probeReleaseFixtureInvocation(t *testing.T, releaseBinary, workDir string) {
	t.Helper()
	probeDir := filepath.Join(workDir, "fixture-probe")
	if err := os.Mkdir(probeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	probeOutput, err := exec.Command(releaseBinary,
		"dev", "scenario", "timeline-empty", "--data-dir", probeDir, "--seed", "1", "--yes", "--no-input", "--json").Output()
	if err != nil {
		t.Fatalf("release fixture invocation probe: %v; stdout=%s", err, probeOutput)
	}
	var probe commandEnvelope[struct {
		Plan struct {
			RecipeVersion string `json:"recipe_version"`
			Counts        Counts `json:"counts"`
		} `json:"plan"`
	}]
	decodeCommandEnvelope(t, probeOutput, &probe)
	if probe.Data.Plan.RecipeVersion != RecipeVersion || probe.Data.Plan.Counts != (Counts{}) {
		t.Fatalf("release fixture invocation probe plan = %#v", probe.Data.Plan)
	}
	probeServerURL, stopProbeServer := startReleasePerformanceServer(t, releaseBinary, probeDir)
	assertHTTPPerformanceFixture(t, &http.Client{Timeout: 5 * time.Second}, probeServerURL, 0)
	stopProbeServer()
	probeReleaseBackupInvocation(t, releaseBinary, workDir, probeDir)
}

func TestReleasePerformanceBinaryProbe(t *testing.T) {
	releaseBinary := os.Getenv("MOMENT_PERFORMANCE_PROBE_BINARY")
	if releaseBinary == "" {
		t.Skip("set MOMENT_PERFORMANCE_PROBE_BINARY to an extracted native release binary")
	}
	if !filepath.IsAbs(releaseBinary) {
		t.Fatal("MOMENT_PERFORMANCE_PROBE_BINARY must be absolute")
	}
	_ = releaseBinaryIdentity(t, releaseBinary, localCommit(t))
	probeReleaseFixtureInvocation(t, releaseBinary, t.TempDir())
}

func qualifyAcceptanceScale(t *testing.T, evidencePath string) {
	t.Helper()
	validationOnly := false
	switch os.Getenv("MOMENT_PERFORMANCE_VALIDATION_ONLY") {
	case "0":
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			t.Fatalf("qualified performance evidence requires linux/amd64, got %s/%s", runtime.GOOS, runtime.GOARCH)
		}
	case "1":
		validationOnly = true
	default:
		t.Fatal("MOMENT_PERFORMANCE_VALIDATION_ONLY must be explicitly set to 0 or 1")
	}
	qualificationStarted := time.Now()
	releaseBinary := os.Getenv("MOMENT_PERFORMANCE_BINARY")
	if releaseBinary == "" || !filepath.IsAbs(releaseBinary) {
		t.Fatal("MOMENT_PERFORMANCE_BINARY must name an absolute extracted release binary")
	}
	workDir := os.Getenv("MOMENT_PERFORMANCE_WORKDIR")
	if workDir == "" || !filepath.IsAbs(workDir) {
		t.Fatal("MOMENT_PERFORMANCE_WORKDIR must name an absolute disposable directory")
	}
	dataDir := filepath.Join(workDir, "data")
	archivePath := filepath.Join(workDir, "acceptance-scale.zip")
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("performance data directory must not exist: %v", err)
	}
	probeReleaseFixtureInvocation(t, releaseBinary, workDir)
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	diskBefore, err := runtimefs.AvailableBytes(workDir)
	if err != nil {
		t.Fatal(err)
	}
	commit := localCommit(t)
	identity := releaseBinaryIdentity(t, releaseBinary, commit)

	fixtureOutput, fixturePeak, fixtureMinimumDisk, fixtureElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"dev", "scenario", "acceptance-scale", "--data-dir", dataDir, "--seed", "1", "--yes", "--no-input", "--json")
	var fixture commandEnvelope[struct {
		Plan struct {
			RecipeVersion string `json:"recipe_version"`
			Counts        Counts `json:"counts"`
		} `json:"plan"`
	}]
	decodeCommandEnvelope(t, fixtureOutput, &fixture)
	expectedCounts := Counts{Entries: 10_000, Comments: 50_000, Media: 20_000, MediaBytes: 20 * gib}
	if fixture.Data.Plan.Counts != expectedCounts || fixture.Data.Plan.RecipeVersion != RecipeVersion {
		t.Fatalf("release fixture plan = %#v", fixture.Data.Plan)
	}
	if fixturePeak >= streamRSSLimit {
		t.Fatalf("fixture peak RSS = %d, limit %d", fixturePeak, streamRSSLimit)
	}
	counts := fixture.Data.Plan.Counts

	serverURL, stopServer := startReleasePerformanceServer(t, releaseBinary, dataDir)
	client := &http.Client{Timeout: 5 * time.Second}
	assertHTTPPerformanceFixture(t, client, serverURL, int(counts.Entries))
	timeline := measureHTTPP95(t, client, serverURL+"/api/v1/posts?limit=20", timelineP95Limit)
	search := measureHTTPP95(t, client, serverURL+"/api/v1/search?q=acceptance&limit=20", searchP95Limit)
	archiveLatency := measureHTTPP95(t, client, serverURL+"/api/v1/archive/months", archiveP95Limit)
	stopServer()

	exportOutput, exportPeak, exportMinimumDisk, exportElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"backup", "export", "--data-dir", dataDir, "--output", archivePath, "--timeout", performanceStageTimeout, "--no-input", "--json")
	var exported commandEnvelope[archive.Summary]
	decodeCommandEnvelope(t, exportOutput, &exported)
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Data.ArchiveBytes != archiveInfo.Size() || archiveInfo.Size() < counts.MediaBytes {
		t.Fatalf("scale export summary=%#v bytes=%d", exported.Data, archiveInfo.Size())
	}
	if exportPeak >= streamRSSLimit {
		t.Fatalf("export peak RSS = %d, limit %d", exportPeak, streamRSSLimit)
	}

	validationDir := filepath.Join(workDir, "import-validation")
	initializeOutput, err := exec.Command(releaseBinary, "owner", "init", "--data-dir", validationDir, "--json").Output()
	if err != nil {
		t.Fatalf("initialize import validation target: %v", err)
	}
	var initialized commandEnvelope[struct {
		Initialized bool `json:"initialized"`
	}]
	decodeCommandEnvelope(t, initializeOutput, &initialized)
	if !initialized.Data.Initialized {
		t.Fatal("release binary did not initialize the import validation target")
	}
	validationBefore := snapshotAuthorityTree(t, validationDir)
	dryRunOutput, validationPeak, validationMinimumDisk, validationElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"backup", "import", archivePath, "--data-dir", validationDir, "--timeout", performanceStageTimeout, "--dry-run", "--json")
	if validationPeak >= streamRSSLimit {
		t.Fatalf("import validation peak RSS = %d, limit %d", validationPeak, streamRSSLimit)
	}
	var restored commandEnvelope[archive.ImportPlan]
	decodeCommandEnvelope(t, dryRunOutput, &restored)
	if restored.Data.ArchiveSHA256 != exported.Data.ArchiveSHA256 ||
		restored.Data.Replacement.Counts.Entries != counts.Entries ||
		restored.Data.Replacement.Counts.Comments != counts.Comments ||
		restored.Data.Replacement.Counts.Media != counts.Media ||
		restored.Data.Replacement.MediaBytes != counts.MediaBytes {
		t.Fatalf("restored authority preview = %#v", restored.Data)
	}
	assertDryRunTargetUnchanged(t, validationBefore, snapshotAuthorityTree(t, validationDir))

	restoreDir := filepath.Join(workDir, "restore")
	initializeOutput, err = exec.Command(releaseBinary, "owner", "init", "--data-dir", restoreDir, "--json").Output()
	if err != nil {
		t.Fatalf("initialize restore target: %v", err)
	}
	decodeCommandEnvelope(t, initializeOutput, &initialized)
	if !initialized.Data.Initialized {
		t.Fatal("release binary did not initialize the restore target")
	}
	importOutput, importPeak, importMinimumDisk, importElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"backup", "import", archivePath, "--data-dir", restoreDir, "--timeout", performanceStageTimeout, "--yes", "--no-input", "--json")
	var imported commandEnvelope[archive.CommitResult]
	decodeCommandEnvelope(t, importOutput, &imported)
	if imported.Data.Status.Phase != archive.CommitSucceeded {
		t.Fatalf("release import phase = %q", imported.Data.Status.Phase)
	}
	if importPeak >= streamRSSLimit {
		t.Fatalf("import peak RSS = %d, limit %d", importPeak, streamRSSLimit)
	}
	doctorOutput, err := exec.Command(releaseBinary, "doctor", "--deep", "--data-dir", restoreDir,
		"--timeout", performanceStageTimeout, "--json").Output()
	if err != nil {
		t.Fatalf("deep doctor restored authority: %v", err)
	}
	assertRestoredDoctorReport(t, doctorOutput)
	restoredServerURL, stopRestoredServer := startReleasePerformanceServer(t, releaseBinary, restoreDir)
	assertHTTPPerformanceFixture(t, &http.Client{Timeout: 5 * time.Second}, restoredServerURL, int(counts.Entries))
	stopRestoredServer()
	diskAfter, err := runtimefs.AvailableBytes(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	evidence := performanceEvidence{SchemaVersion: 1, Qualified: !validationOnly, ValidationOnly: validationOnly, Commit: commit}
	evidence.TotalElapsedMS = time.Since(qualificationStarted).Milliseconds()
	evidence.FixtureElapsedMS = fixtureElapsed.Milliseconds()
	evidence.Environment.GOOS = runtime.GOOS
	evidence.Environment.GOARCH = runtime.GOARCH
	evidence.Environment.GoVersion = runtime.Version()
	evidence.Environment.GOMAXPROCS = runtime.GOMAXPROCS(0)
	evidence.Environment.RSSSampleIntervalMS = 25
	evidence.Environment.DiskAvailableBeforeBytes = diskBefore
	evidence.Environment.DiskAvailableAfterBytes = diskAfter
	evidence.Environment.MinimumDiskAvailableBytes = min(fixtureMinimumDisk, exportMinimumDisk, importMinimumDisk, validationMinimumDisk, diskAfter)
	evidence.Environment.PeakDiskUsedBytes = diskBefore - evidence.Environment.MinimumDiskAvailableBytes
	evidence.Binary = identity
	evidence.Fixture.ProbeScenario = "timeline-empty"
	evidence.Fixture.ProbeStageTimeout = performanceStageTimeout
	evidence.Fixture.ProbeTimeoutNoPartial = true
	evidence.Fixture.ProbeArchiveRoundTrip = true
	evidence.Fixture.ProbeDeepDoctor = true
	evidence.Fixture.RecipeVersion = fixture.Data.Plan.RecipeVersion
	evidence.Fixture.Entries = counts.Entries
	evidence.Fixture.Comments = counts.Comments
	evidence.Fixture.Media = counts.Media
	evidence.Fixture.MediaBytes = counts.MediaBytes
	evidence.Fixture.PeakRSSBytes = fixturePeak
	evidence.Fixture.RSSLimitBytes = streamRSSLimit
	evidence.Latency.Timeline = timeline
	evidence.Latency.Search = search
	evidence.Latency.Archive = archiveLatency
	evidence.Streaming.Export = streamMetric{Bytes: archiveInfo.Size(), PeakRSSBytes: exportPeak, LimitBytes: streamRSSLimit, ElapsedMS: exportElapsed.Milliseconds()}
	evidence.Streaming.Import = streamMetric{Bytes: archiveInfo.Size(), PeakRSSBytes: importPeak, LimitBytes: streamRSSLimit, ElapsedMS: importElapsed.Milliseconds()}
	evidence.Streaming.ImportValidation = importValidationMetric{
		streamMetric:                 streamMetric{Bytes: archiveInfo.Size(), PeakRSSBytes: validationPeak, LimitBytes: streamRSSLimit, ElapsedMS: validationElapsed.Milliseconds()},
		TargetState:                  "initialized-empty",
		TargetUnchanged:              true,
		ValidationBeforeActualImport: true,
	}
	evidence.ArchiveSHA256 = exported.Data.ArchiveSHA256
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(evidencePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startReleasePerformanceServer(t *testing.T, binary, dataDir string) (string, func()) {
	t.Helper()
	_ = releaseBinaryIdentity(t, binary, localCommit(t))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "serve", "--data-dir", dataDir, "--listen", address, "--log-format", "json")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, requestErr := client.Get("http://" + address + "/readyz")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("release server did not become ready: %v; stderr=%s", requestErr, stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if err := command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- command.Wait() }()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("release server exit: %v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
			}
		case <-time.After(15 * time.Second):
			_ = command.Process.Kill()
			t.Fatal("release server did not stop gracefully")
		}
		if !strings.Contains(stderr.String(), `"event":"server_stopped"`) {
			t.Fatalf("release server did not log graceful stop: %s", stderr.String())
		}
	}
	t.Cleanup(stop)
	return "http://" + address, stop
}

func assertHTTPPerformanceFixture(t *testing.T, client *http.Client, baseURL string, expectedEntries int) {
	t.Helper()
	type counted struct {
		Count int `json:"count"`
	}
	type responseMeta struct {
		RequestID string `json:"request_id"`
	}
	expectedPageItems := min(20, expectedEntries)
	var timeline struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
		Meta responseMeta `json:"meta"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/posts?limit=20", &timeline)
	if len(timeline.Data.Items) != expectedPageItems || strings.TrimSpace(timeline.Meta.RequestID) == "" {
		t.Fatalf("timeline HTTP fixture = items:%d request_id:%q", len(timeline.Data.Items), timeline.Meta.RequestID)
	}
	var search struct {
		Data struct {
			Items      []json.RawMessage `json:"items"`
			YearCounts []counted         `json:"year_counts"`
		} `json:"data"`
		Meta responseMeta `json:"meta"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/search?q=acceptance&limit=20", &search)
	var searchCount int
	for _, item := range search.Data.YearCounts {
		searchCount += item.Count
	}
	if len(search.Data.Items) != expectedPageItems || searchCount != expectedEntries || strings.TrimSpace(search.Meta.RequestID) == "" {
		t.Fatalf("search HTTP fixture = items:%d count:%d request_id:%q", len(search.Data.Items), searchCount, search.Meta.RequestID)
	}
	var months struct {
		Data struct {
			Items []counted `json:"items"`
		} `json:"data"`
		Meta responseMeta `json:"meta"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/archive/months", &months)
	var archiveCount int
	for _, item := range months.Data.Items {
		archiveCount += item.Count
	}
	if archiveCount != expectedEntries || strings.TrimSpace(months.Meta.RequestID) == "" {
		t.Fatalf("archive HTTP fixture = count:%d request_id:%q", archiveCount, months.Meta.RequestID)
	}
}

func readHTTPJSON(t *testing.T, client *http.Client, endpoint string, destination any) {
	t.Helper()
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d", endpoint, response.StatusCode)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET %s content type = %q", endpoint, response.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 1<<20 {
		t.Fatalf("GET %s response exceeded 1 MiB", endpoint)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 2 || envelope["data"] == nil || envelope["meta"] == nil {
		t.Fatalf("GET %s response must contain exactly data and meta", endpoint)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatal(err)
	}
}

func measureHTTPP95(t *testing.T, client *http.Client, endpoint string, limit time.Duration) performanceMetric {
	t.Helper()
	return measureP95(t, func() error {
		response, err := client.Get(endpoint)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s returned %d: %s", endpoint, response.StatusCode, body)
		}
		if len(body) > 1<<20 {
			t.Fatalf("GET %s response exceeded 1 MiB", endpoint)
		}
		return nil
	}, limit)
}

func measureP95(t *testing.T, operation func() error, limit time.Duration) performanceMetric {
	t.Helper()
	for range performanceWarmup {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	samples := make([]time.Duration, 0, performanceRuns)
	for range performanceRuns {
		started := time.Now()
		if err := operation(); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(started))
	}
	slices.Sort(samples)
	p95 := samples[(len(samples)*95+99)/100-1]
	if p95 >= limit {
		t.Fatalf("p95 = %s, limit %s", p95, limit)
	}
	return performanceMetric{Samples: len(samples), P95MS: float64(p95) / float64(time.Millisecond), LimitMS: float64(limit) / float64(time.Millisecond)}
}
