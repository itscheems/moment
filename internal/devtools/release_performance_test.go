package devtools

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/runtimefs"
)

const (
	timelineP95Limit  = 100 * time.Millisecond
	searchP95Limit    = 200 * time.Millisecond
	archiveP95Limit   = 250 * time.Millisecond
	streamRSSLimit    = uint64(768 << 20)
	performanceWarmup = 5
	performanceRuns   = 31
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
		RecipeVersion string `json:"recipe_version"`
		Entries       int64  `json:"entries"`
		Comments      int64  `json:"comments"`
		Media         int64  `json:"media"`
		MediaBytes    int64  `json:"media_bytes"`
		PeakRSSBytes  uint64 `json:"peak_rss_bytes"`
		RSSLimitBytes uint64 `json:"rss_limit_bytes"`
	} `json:"fixture"`
	Latency struct {
		Timeline performanceMetric `json:"timeline"`
		Search   performanceMetric `json:"search"`
		Archive  performanceMetric `json:"archive"`
	} `json:"latency"`
	Streaming struct {
		Export           streamMetric `json:"export"`
		Import           streamMetric `json:"import"`
		ImportValidation streamMetric `json:"import_validation"`
	} `json:"streaming"`
	ArchiveSHA256    string `json:"archive_sha256"`
	FixtureElapsedMS int64  `json:"fixture_elapsed_ms"`
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
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4096 {
			message = message[len(message)-4096:]
		}
		t.Fatalf("release command %q failed after %s: %v; stderr=%s", arguments[:3], elapsed, err, message)
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

func qualifyAcceptanceScale(t *testing.T, evidencePath string) {
	t.Helper()
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
		"backup", "export", "--data-dir", dataDir, "--output", archivePath, "--no-input", "--json")
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

	restoreDir := filepath.Join(workDir, "restore")
	initializeOutput, err := exec.Command(releaseBinary, "owner", "init", "--data-dir", restoreDir, "--json").Output()
	if err != nil {
		t.Fatalf("initialize restore target: %v", err)
	}
	var initialized commandEnvelope[struct {
		Initialized bool `json:"initialized"`
	}]
	decodeCommandEnvelope(t, initializeOutput, &initialized)
	if !initialized.Data.Initialized {
		t.Fatal("release binary did not initialize the restore target")
	}
	importOutput, importPeak, importMinimumDisk, importElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"backup", "import", archivePath, "--data-dir", restoreDir, "--yes", "--no-input", "--json")
	var imported commandEnvelope[archive.CommitResult]
	decodeCommandEnvelope(t, importOutput, &imported)
	if imported.Data.Status.Phase != archive.CommitSucceeded {
		t.Fatalf("release import phase = %q", imported.Data.Status.Phase)
	}
	if importPeak >= streamRSSLimit {
		t.Fatalf("import peak RSS = %d, limit %d", importPeak, streamRSSLimit)
	}

	dryRunOutput, validationPeak, validationMinimumDisk, validationElapsed := runMeasuredReleaseCommand(t, releaseBinary, workDir,
		"backup", "import", archivePath, "--data-dir", restoreDir, "--dry-run", "--json")
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
	doctorOutput, err := exec.Command(releaseBinary, "doctor", "--deep", "--data-dir", restoreDir, "--json").Output()
	if err != nil {
		t.Fatalf("deep doctor restored authority: %v", err)
	}
	var doctor commandEnvelope[struct {
		Status string `json:"status"`
	}]
	decodeCommandEnvelope(t, doctorOutput, &doctor)
	if doctor.Data.Status != "healthy" {
		t.Fatalf("restored authority doctor status = %q", doctor.Data.Status)
	}
	restoredServerURL, stopRestoredServer := startReleasePerformanceServer(t, releaseBinary, restoreDir)
	assertHTTPPerformanceFixture(t, &http.Client{Timeout: 5 * time.Second}, restoredServerURL, int(counts.Entries))
	stopRestoredServer()
	diskAfter, err := runtimefs.AvailableBytes(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	evidence := performanceEvidence{SchemaVersion: 1, Qualified: true, Commit: commit}
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
	evidence.Streaming.ImportValidation = streamMetric{Bytes: archiveInfo.Size(), PeakRSSBytes: validationPeak, LimitBytes: streamRSSLimit, ElapsedMS: validationElapsed.Milliseconds()}
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
	var timeline struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/posts?limit=20", &timeline)
	if !timeline.OK || len(timeline.Data.Items) != 20 {
		t.Fatalf("timeline HTTP fixture = ok:%v items:%d", timeline.OK, len(timeline.Data.Items))
	}
	var search struct {
		OK   bool `json:"ok"`
		Data struct {
			Items      []json.RawMessage `json:"items"`
			YearCounts []counted         `json:"year_counts"`
		} `json:"data"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/search?q=acceptance&limit=20", &search)
	var searchCount int
	for _, item := range search.Data.YearCounts {
		searchCount += item.Count
	}
	if !search.OK || len(search.Data.Items) != 20 || searchCount != expectedEntries {
		t.Fatalf("search HTTP fixture = ok:%v items:%d count:%d", search.OK, len(search.Data.Items), searchCount)
	}
	var months struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []counted `json:"items"`
		} `json:"data"`
	}
	readHTTPJSON(t, client, baseURL+"/api/v1/archive/months", &months)
	var archiveCount int
	for _, item := range months.Data.Items {
		archiveCount += item.Count
	}
	if !months.OK || archiveCount != expectedEntries {
		t.Fatalf("archive HTTP fixture = ok:%v count:%d", months.OK, archiveCount)
	}
}

func readHTTPJSON(t *testing.T, client *http.Client, endpoint string, destination any) {
	t.Helper()
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	decoder := json.NewDecoder(io.LimitReader(response.Body, (1<<20)+1))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d", endpoint, response.StatusCode)
	}
	if err := decoder.Decode(destination); err != nil {
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
