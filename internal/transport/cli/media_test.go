package cli

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func TestMediaDirectAgentContractAndMaintenanceSafety(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	imagePath := writeCLIImage(t)
	application := momentTestApplication()
	addedOutput := execute(application, "", "media", "add", imagePath, "--data-dir", dataDir, "--json")
	if addedOutput.exit != ExitSuccess || addedOutput.stderr != "" {
		t.Fatalf("add = %#v", addedOutput)
	}
	added := commandData(t, addedOutput)
	mediaData := added["media"].(map[string]any)
	id := mediaData["id"].(string)
	if id == "" || mediaData["storage_path"] != nil || added["preview_url"] != "/api/v1/media/"+id+"/preview" {
		t.Fatalf("added = %#v", added)
	}

	listed := commandData(t, execute(application, "", "media", "list", "--data-dir", dataDir, "--json"))
	items := listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["referenced"] != false || items[0].(map[string]any)["reference_consistent"] != true {
		t.Fatalf("list = %#v", listed)
	}
	verified := commandData(t, execute(application, "", "media", "verify", id, "--data-dir", dataDir, "--json"))
	if verified["total"] != float64(1) || verified["healthy"] != float64(1) {
		t.Fatalf("verify = %#v", verified)
	}

	outputPath := filepath.Join(t.TempDir(), "original.png")
	got := commandData(t, execute(application, "", "media", "get", id, "--output", outputPath, "--data-dir", dataDir, "--json"))
	if got["id"] != id || got["output_path"] != outputPath {
		t.Fatalf("get = %#v", got)
	}
	if contents, err := os.ReadFile(outputPath); err != nil || !bytes.Equal(contents, mustRead(t, imagePath)) {
		t.Fatalf("downloaded original = %d bytes, %v", len(contents), err)
	}
	conflict := execute(application, "", "media", "get", id, "--output", outputPath, "--data-dir", dataDir, "--json")
	if conflict.exit != ExitIO || decodeOneDocument(t, conflict.stdout)["error"].(map[string]any)["code"] != "MEDIA_IO_FAILED" {
		t.Fatalf("get conflict = %#v", conflict)
	}

	dryRebuild := execute(application, "", "media", "rebuild-previews", "--dry-run", "--data-dir", dataDir, "--json")
	if dryRebuild.exit != ExitSuccess || commandData(t, dryRebuild)["dry_run"] != true {
		t.Fatalf("dry rebuild = %#v", dryRebuild)
	}
	unconfirmed := execute(application, "", "media", "rebuild-previews", "--data-dir", dataDir, "--json")
	if unconfirmed.exit != ExitInvalidInput || decodeOneDocument(t, unconfirmed.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("unconfirmed rebuild = %#v", unconfirmed)
	}
	confirmed := execute(application, "", "media", "rebuild-previews", "--yes", "--data-dir", dataDir, "--json")
	if confirmed.exit != ExitSuccess {
		t.Fatalf("confirmed rebuild = %#v", confirmed)
	}
	exclusive, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.MediaLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	blocked := execute(application, "", "media", "list", "--timeout", "200ms", "--data-dir", dataDir, "--json")
	if err := exclusive.Release(); err != nil {
		t.Fatal(err)
	}
	if blocked.exit != ExitTimeout || decodeOneDocument(t, blocked.stdout)["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("exclusive lock conflict = %#v", blocked)
	}

	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET created_at_ms = created_at_ms - 172800000, unreferenced_since_ms = unreferenced_since_ms - 172800000 WHERE id = ?`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	plan := commandData(t, execute(application, "", "media", "prune", "--dry-run", "--data-dir", dataDir, "--json"))
	if plan["eligible"] != float64(1) || plan["deleted"] != float64(0) {
		t.Fatalf("prune plan = %#v", plan)
	}
	pruned := commandData(t, execute(application, "", "media", "prune", "--yes", "--data-dir", dataDir, "--json"))
	if pruned["deleted"] != float64(1) {
		t.Fatalf("pruned = %#v", pruned)
	}
	missing := execute(application, "", "media", "get", id, "--output", filepath.Join(t.TempDir(), "missing.png"), "--data-dir", dataDir, "--json")
	if missing.exit != ExitNotFound || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "MEDIA_NOT_FOUND" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestMediaDirectAPIParityAndUnsupportedMaintenanceModes(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := auth.NewManager(runtimeState.Root, auth.Options{})
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	store, err := media.NewStore(runtimeState, database, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, OwnerAuth: manager, Media: store, Readiness: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	application := momentTestApplication()
	imagePath := writeCLIImage(t)
	direct := commandData(t, execute(application, "", "media", "add", imagePath, "--data-dir", dataDir, "--json"))
	t.Setenv("MOMENT_TOKEN", credential.Token)
	api := commandData(t, execute(application, "", "--url", server.URL, "media", "add", imagePath, "--json"))
	if !reflect.DeepEqual(direct, api) {
		t.Fatalf("direct = %#v\napi = %#v", direct, api)
	}
	id := direct["media"].(map[string]any)["id"].(string)
	directOutput := filepath.Join(t.TempDir(), "direct.png")
	apiOutput := filepath.Join(t.TempDir(), "api.png")
	directGet := commandData(t, execute(application, "", "media", "get", id, "--output", directOutput, "--data-dir", dataDir, "--json"))
	apiGet := commandData(t, execute(application, "", "--url", server.URL, "media", "get", id, "--output", apiOutput, "--json"))
	directGet["output_path"] = "normalized"
	apiGet["output_path"] = "normalized"
	if !reflect.DeepEqual(directGet, apiGet) || !bytes.Equal(mustRead(t, directOutput), mustRead(t, apiOutput)) {
		t.Fatalf("direct get = %#v api get = %#v", directGet, apiGet)
	}

	for _, command := range []string{"verify", "list", "rebuild-previews", "prune"} {
		args := []string{"--url", server.URL, "media", command, "--json"}
		if command == "verify" {
			args = []string{"--url", server.URL, "media", command, id, "--json"}
		}
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
			t.Fatalf("%s API mode = %#v", command, output)
		}
	}
}

func TestMediaHelpAndCancellationContract(t *testing.T) {
	_, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exit := application.Execute(ctx, []string{"media", "list", "--data-dir", dataDir, "--json"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitTimeout || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" || stderr.Len() != 0 {
		t.Fatalf("cancelled = %d %q %q", exit, stdout.String(), stderr.String())
	}
	for _, command := range []string{"add", "get", "verify", "list", "rebuild-previews", "prune"} {
		output := execute(application, "", "media", command, "--help")
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("help %s = %#v", command, output)
		}
		for _, required := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:", "--json"} {
			if !strings.Contains(output.stdout, required) {
				t.Fatalf("help %s missing %q:\n%s", command, required, output.stdout)
			}
		}
	}
}

func TestDirectMediaHoldsCurrentUntilCommandRuntimeCloses(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	originalGeneration := runtimeState.Current.ID
	creation, err := runtimeState.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := runtimeState.CreateGeneration(context.Background(), "replacement-generation", creation)
	if releaseErr := creation.Release(); err == nil && releaseErr != nil {
		err = releaseErr
	}
	if err != nil {
		t.Fatal(err)
	}

	direct, err := openDirectMedia(context.Background(), Invocation{target: directTarget{dataDir: dataDir}})
	if err != nil {
		t.Fatal(err)
	}
	if !direct.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		t.Fatal("direct media runtime did not retain maintenance shared")
	}

	switchDone := make(chan error, 1)
	go func() {
		leases, acquireErr := runtimeState.Locks.AcquireSet(context.Background(),
			runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
		)
		if acquireErr != nil {
			switchDone <- acquireErr
			return
		}
		switchErr := runtimeState.SwitchCurrent(context.Background(), replacement.ID, leases)
		switchDone <- errors.Join(switchErr, leases.Release())
	}()

	deadline := time.Now().Add(time.Second)
	for {
		probeContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		probe, probeErr := runtimeState.Locks.Acquire(probeContext, runtimefs.ImportLock, runtimefs.Exclusive)
		cancel()
		if probeErr != nil {
			if !errors.Is(probeErr, context.DeadlineExceeded) {
				t.Fatal(probeErr)
			}
			break
		}
		_ = probe.Release()
		if time.Now().After(deadline) {
			t.Fatal("competing CURRENT switch did not reach the maintenance boundary")
		}
	}

	imagePath := writeCLIImage(t)
	file, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, ingestErr := direct.store.Ingest(context.Background(), filepath.Base(imagePath), file)
	closeFileErr := file.Close()
	if err := errors.Join(ingestErr, closeFileErr); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-switchDone:
		t.Fatalf("CURRENT switched before direct command closed: %v", err)
	default:
	}
	if current, err := runtimeState.ResolveCurrent(); err != nil || current.ID != originalGeneration {
		t.Fatalf("CURRENT changed during direct operation: %v, %v", current, err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-switchDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("CURRENT switch did not resume after direct command closed")
	}
	current, err := runtimefs.OpenContext(context.Background(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if current.Current.ID != replacement.ID {
		t.Fatalf("CURRENT = %s, want %s", current.Current.ID, replacement.ID)
	}
}

func TestWriteNewFileAtomicNeverClobbersCompetingOutput(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "download.bin")
	const competitors = 16
	type outcome struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan outcome, competitors)
	var group sync.WaitGroup
	for index := 0; index < competitors; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := writeNewFileAtomic(context.Background(), destination, bytes.Repeat([]byte{byte(index + 1)}, 64))
			results <- outcome{index: index, err: err}
		}(index)
	}
	close(start)
	group.Wait()
	close(results)
	winner := -1
	for result := range results {
		if result.err == nil {
			if winner != -1 {
				t.Fatalf("multiple no-clobber writers succeeded: %d and %d", winner, result.index)
			}
			winner = result.index
		}
	}
	if winner == -1 {
		t.Fatal("no no-clobber writer succeeded")
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, bytes.Repeat([]byte{byte(winner + 1)}, 64)) {
		t.Fatal("published output does not belong to the sole successful writer")
	}
	partials, err := filepath.Glob(filepath.Join(directory, ".moment-media-*.partial"))
	if err != nil || len(partials) != 0 {
		t.Fatalf("partial downloads remain: %v, %v", partials, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeNewFileAtomic(cancelled, destination, []byte("cancelled")); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	if contents, err := os.ReadFile(destination); err != nil || !bytes.Equal(contents, bytes.Repeat([]byte{byte(winner + 1)}, 64)) {
		t.Fatalf("cancelled publication changed winner: %d bytes, %v", len(contents), err)
	}
}

func writeCLIImage(t *testing.T) string {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	for index := range value.Pix {
		value.Pix[index] = 0x88
	}
	value.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0})
	path := filepath.Join(t.TempDir(), "image.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, value); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
