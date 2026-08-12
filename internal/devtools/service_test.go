package devtools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestExecuteScenarioPublishesDoctorHealthyFixture(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	result, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "timeline-basic", Seed: 41,
	})
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			t.Fatalf("%v details=%v", err, domainErr.Details)
		}
		t.Fatal(err)
	}
	if !result.Applied || !result.DoctorHealthy || result.Plan.Counts.Entries != 25 || result.OwnerToken == "" {
		t.Fatalf("result = %#v", result)
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if current.ID == oldID {
		t.Fatal("CURRENT did not publish the fixture generation")
	}
}

func TestExecuteArchiveRoundTripUsesFormalPortableContract(t *testing.T) {
	runtimeState := developmentRuntime(t)
	result, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "archive-roundtrip", Seed: 17,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.DoctorHealthy || !result.ArchiveRoundTripPassed || result.OwnerToken == "" {
		t.Fatalf("roundtrip result = %#v", result)
	}
	currentRuntime, err := runtimefs.OpenContext(context.Background(), runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), currentRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var usages int
	if err := database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM comment_media`).Scan(&usages)
	}); err != nil {
		t.Fatal(err)
	}
	if usages == 0 {
		t.Fatal("archive roundtrip fixture omitted comment media")
	}
}

func TestArchiveRoundTripFailureAfterFormalCommitRollsBackSource(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	injected := errors.New("roundtrip post-commit failure")
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "archive-roundtrip", Seed: 17,
		hooks: &operationHooks{AfterRoundTripCommit: func() error { return injected }},
	})
	if !errors.Is(err, injected) {
		t.Fatalf("Execute error = %v, want injected", err)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("source CURRENT after roundtrip failure = %q, err=%v, want %q", current.ID, resolveErr, oldID)
	}
}

func TestArchiveRoundTripTargetSwapDoesNotTouchOutside(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	outside := canonicalTempDir(t)
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "archive-roundtrip", Seed: 17,
		hooks: &operationHooks{AfterRoundTripTarget: func(target string) error {
			if err := os.Rename(target, target+"-saved"); err != nil {
				return err
			}
			return os.Symlink(outside, target)
		}},
	})
	if err == nil {
		t.Fatal("roundtrip target swap was accepted")
	}
	data, readErr := os.ReadFile(canary)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("outside canary changed: data=%q err=%v", data, readErr)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("source CURRENT after target swap = %q, err=%v, want %q", current.ID, resolveErr, oldID)
	}
}

func TestArchiveRoundTripCleanupRemovesSwappedLinkOnly(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	outside := canonicalTempDir(t)
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var swappedLeaf string
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "archive-roundtrip", Seed: 17,
		hooks: &operationHooks{BeforeRoundTripCleanup: func(workspace string) error {
			swappedLeaf = workspace
			if err := os.Rename(workspace, workspace+"-saved"); err != nil {
				return err
			}
			return os.Symlink(outside, workspace)
		}},
	})
	if err == nil {
		t.Fatal("cleanup identity swap was reported as success")
	}
	if _, err := os.Lstat(swappedLeaf); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("swapped cleanup link remains: %v", err)
	}
	data, readErr := os.ReadFile(canary)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("outside canary changed: data=%q err=%v", data, readErr)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("source CURRENT after cleanup swap = %q, err=%v, want %q", current.ID, resolveErr, oldID)
	}
}

func TestArchiveRoundTripCancellationAfterCommitRollsBackSource(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Execute(ctx, Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "archive-roundtrip", Seed: 17,
		hooks: &operationHooks{AfterRoundTripCommit: func() error {
			cancel()
			return ctx.Err()
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want cancellation", err)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("source CURRENT after cancellation = %q, err=%v, want %q", current.ID, resolveErr, oldID)
	}
}

func TestScenarioSeedIsLogicallyDeterministicAcrossInstances(t *testing.T) {
	digest := func(seed int64) string {
		t.Helper()
		runtimeState := developmentRuntime(t)
		if _, err := Execute(context.Background(), Request{DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "search-cjk", Seed: seed}); err != nil {
			t.Fatal(err)
		}
		exporter, err := archive.NewExporter(archive.Options{DataDir: runtimeState.Root, AppVersion: RecipeVersion})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if _, err := exporter.Export(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		value, err := portableSemanticDigest(output.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := digest(71)
	if second := digest(71); second != first {
		t.Fatalf("same seed drifted: %s != %s", first, second)
	}
	if different := digest(72); different == first {
		t.Fatalf("different seed did not change logical fixture: %s", first)
	}
}

func TestScaleCatalogAndPlannerAreBoundedAtAcceptanceTotals(t *testing.T) {
	for _, name := range []string{"acceptance-scale", "release-scale"} {
		recipe, err := LookupScenario(name)
		if err != nil {
			t.Fatal(err)
		}
		want := Counts{Entries: 10_000, Comments: 50_000, Media: 20_000, MediaBytes: 20 * gib}
		if recipe.Counts != want {
			t.Fatalf("%s counts = %+v, want %+v", name, recipe.Counts, want)
		}
		var comments, mediaRecords, mediaBytes, maxPerEntry, maxMediaBytes int64
		for entry := int64(0); entry < recipe.Counts.Entries; entry++ {
			comments += allocation(recipe.Counts.Comments, recipe.Counts.Entries, entry)
			mediaForEntry, _ := mediaAllocation(recipe, entry)
			if mediaForEntry > maxPerEntry {
				maxPerEntry = mediaForEntry
			}
			for local := int64(0); local < mediaForEntry; local++ {
				size := allocatedBytes(recipe.Counts.MediaBytes, recipe.Counts.Media, mediaRecords)
				mediaRecords++
				mediaBytes += size
				if size > maxMediaBytes {
					maxMediaBytes = size
				}
			}
		}
		if comments != want.Comments || mediaRecords != want.Media || mediaBytes != want.MediaBytes {
			t.Fatalf("%s planner totals comments=%d media=%d bytes=%d", name, comments, mediaRecords, mediaBytes)
		}
		if maxPerEntry > 2 || maxMediaBytes > 2<<20 {
			t.Fatalf("%s planner is not batch bounded: max entry media=%d max blob=%d", name, maxPerEntry, maxMediaBytes)
		}
	}
}

func TestEveryNamedNonScaleScenarioHasExpectedDoctorHandoff(t *testing.T) {
	for _, scenario := range Catalog() {
		if scenario.Category == CategoryPerformance {
			continue
		}
		t.Run(scenario.Name, func(t *testing.T) {
			runtimeState := developmentRuntime(t)
			result, err := Execute(context.Background(), Request{
				DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: scenario.Name, Seed: 51,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Applied || result.Plan.Counts != scenario.Counts || result.ExpectedDoctorCheckID != scenario.ExpectedDoctorCheckID {
				t.Fatalf("scenario result = %#v", result)
			}
			diagnostic, err := doctor.New(doctor.Options{DataDir: runtimeState.Root})
			if err != nil {
				t.Fatal(err)
			}
			report, err := diagnostic.Run(context.Background(), doctor.ModeDeep)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateDoctorHandoff(report, scenario.ExpectedDoctorCheckID, false); err != nil {
				t.Fatalf("external doctor handoff status=%s: %v", report.Status, err)
			}
		})
	}
}

func TestAcceptanceScaleEndToEnd(t *testing.T) {
	if os.Getenv("MOMENT_RUN_DEV_SCALE") != "1" {
		t.Skip("set MOMENT_RUN_DEV_SCALE=1 on a disposable volume with at least 24 GiB free")
	}
	if evidencePath := os.Getenv("MOMENT_PERFORMANCE_EVIDENCE"); evidencePath != "" {
		qualifyAcceptanceScale(t, evidencePath)
		return
	}
	runtimeState := developmentRuntime(t)
	result, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "acceptance-scale", Seed: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Plan.Counts != (Counts{Entries: 10_000, Comments: 50_000, Media: 20_000, MediaBytes: 20 * gib}) {
		t.Fatalf("acceptance-scale result = %#v", result)
	}
}

func TestAcceptanceScaleCancellationKeepsOldCurrent(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Execute(ctx, Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "acceptance-scale", Seed: 9,
		Progress: func(progress Progress) {
			if progress.Kind == "media" && progress.Completed == 1 {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want cancellation", err)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("CURRENT after scale cancellation = %q, err=%v, want %q", current.ID, resolveErr, oldID)
	}
}

func TestExecuteDryRunIsReadOnlyAndStillChecksServeLock(t *testing.T) {
	runtimeState := developmentRuntime(t)
	before, err := os.ReadFile(filepath.Join(runtimeState.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationReset, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(runtimeState.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied || string(before) != string(after) {
		t.Fatalf("dry run mutated CURRENT: result=%#v", result)
	}
	serve, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serve.Release() }()
	if _, err := Execute(context.Background(), Request{DataDir: runtimeState.Root, Operation: OperationReset, DryRun: true}); err == nil {
		t.Fatal("dry run ignored active serve lock")
	}
}

func TestExecuteRollsBackPostSwitchFailure(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	injected := errors.New("post-switch failure")
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "timeline-empty", Seed: 1,
		hooks: &operationHooks{AfterSwitch: func(runtimefs.Generation) error { return injected }},
	})
	if !errors.Is(err, injected) {
		t.Fatalf("Execute error = %v, want injected", err)
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if current.ID != oldID {
		t.Fatalf("CURRENT = %s, want rollback %s", current.ID, oldID)
	}
}

func TestExecuteRejectsGenerationSwapWithoutTouchingOutside(t *testing.T) {
	runtimeState := developmentRuntime(t)
	outside := canonicalTempDir(t)
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "timeline-empty", Seed: 1,
		hooks: &operationHooks{AfterCreate: func(generation runtimefs.Generation) error {
			if err := os.Rename(generation.Root, generation.Root+"-saved"); err != nil {
				return err
			}
			return os.Symlink(outside, generation.Root)
		}},
	})
	if err == nil {
		t.Fatal("generation swap was accepted")
	}
	data, readErr := os.ReadFile(canary)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("outside canary changed: data=%q err=%v", data, readErr)
	}
}

func TestBootstrapRejectsConcurrentMember(t *testing.T) {
	root := canonicalTempDir(t)
	canary := filepath.Join(root, "concurrent")
	_, err := Execute(context.Background(), Request{
		DataDir: root, Operation: OperationScenario, Scenario: "timeline-empty", Seed: 1,
		hooks: &operationHooks{AfterBootstrap: func() error {
			return os.WriteFile(canary, []byte("do not overwrite"), 0o600)
		}},
	})
	if err == nil {
		t.Fatal("concurrent bootstrap member was accepted")
	}
	data, readErr := os.ReadFile(canary)
	if readErr != nil || string(data) != "do not overwrite" {
		t.Fatalf("bootstrap canary changed: data=%q err=%v", data, readErr)
	}
}

func TestBootstrapConcurrentCommandsShareOneInstanceAndOneOwnerDelivery(t *testing.T) {
	root := canonicalTempDir(t)
	start := make(chan struct{})
	results := make([]Result, 2)
	errorsSeen := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsSeen[index] = Execute(context.Background(), Request{
				DataDir: root, Operation: OperationScenario, Scenario: "timeline-basic", Seed: 33,
			})
		}(index)
	}
	close(start)
	wait.Wait()
	tokens, successes, conflicts := 0, 0, 0
	for index, err := range errorsSeen {
		if err == nil {
			successes++
			if results[index].OwnerToken != "" {
				tokens++
			}
			continue
		}
		if domain.HasCode(err, domain.CodeInstanceInUse) {
			conflicts++
			continue
		}
		t.Fatalf("concurrent bootstrap %d error = %v, want stable conflict", index, err)
	}
	if successes != 1 || conflicts != 1 || tokens != 1 {
		t.Fatalf("concurrent bootstrap successes=%d conflicts=%d owner deliveries=%d", successes, conflicts, tokens)
	}
	instance, err := runtimefs.ReadInstance(root)
	if err != nil || instance.Environment != runtimefs.EnvironmentDevelopment {
		t.Fatalf("concurrent bootstrap marker = %#v err=%v", instance, err)
	}
}

func TestBootstrapCancelledCheckpointIsRetryable(t *testing.T) {
	root := canonicalTempDir(t)
	bootstrap, err := OpenBootstrapTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := bootstrap.OpenAuthority()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, initializeErr := runtimefs.InitializeDevelopmentRoot(ctx, root, authority)
	if closeErr := authority.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(initializeErr, context.Canceled) {
		t.Fatalf("InitializeDevelopmentRoot error = %v, want cancellation", initializeErr)
	}
	result, err := Execute(context.Background(), Request{
		DataDir: root, Operation: OperationScenario, Scenario: "timeline-basic", Seed: 33,
	})
	if err != nil || !result.Applied || result.OwnerToken == "" {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	diagnostic, err := doctor.New(doctor.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := diagnostic.Run(context.Background(), doctor.ModeDeep)
	if err != nil || report.Counts.Fail != 0 {
		t.Fatalf("retry doctor status=%s failures=%d err=%v", report.Status, report.Counts.Fail, err)
	}
}

func TestDevelopmentCapabilityRejectsDifferentRootWithoutAuthSideEffect(t *testing.T) {
	runtimeState := developmentRuntime(t)
	target, err := OpenTarget(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	coordinated, held, root, err := openCoordinatedTarget(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release(); _ = root.Close() }()
	expected := targetGenerationAuthority(target)
	pinned, err := openPinnedRuntimePaths(root, target, expected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pinned.Close() }()
	capability, err := runtimefs.BindDevelopmentAuthority(coordinated, root, pinned.generationRoot, pinned.databasePath, held)
	if err != nil {
		t.Fatal(err)
	}
	outside := canonicalTempDir(t)
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrongRuntime := *coordinated
	wrongRuntime.Root = outside
	if _, err := auth.InitializeDevelopmentWithinLocks(context.Background(), &wrongRuntime, capability, held); !errors.Is(err, runtimefs.ErrInsufficientLocks) {
		t.Fatalf("mismatched authority error = %v", err)
	}
	data, err := os.ReadFile(canary)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside canary changed: data=%q err=%v", data, err)
	}
	outsideRoot, err := os.OpenRoot(outside)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outsideRoot.Close() }()
	if _, err := runtimefs.BindDevelopmentAuthority(coordinated, outsideRoot, outsideRoot, canary, held); !errors.Is(err, runtimefs.ErrInsufficientLocks) {
		t.Fatalf("arbitrary capability bind error = %v", err)
	}
}

func TestSwitchRejectsSwapAfterFinalVerification(t *testing.T) {
	runtimeState := developmentRuntime(t)
	oldID := runtimeState.Current.ID
	outside := canonicalTempDir(t)
	canary := filepath.Join(outside, "canary")
	if err := os.WriteFile(canary, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), Request{
		DataDir: runtimeState.Root, Operation: OperationScenario, Scenario: "timeline-empty", Seed: 1,
		hooks: &operationHooks{AfterFinalSwitchVerify: func(generation runtimefs.Generation) error {
			if err := os.Rename(generation.Root, generation.Root+"-saved"); err != nil {
				return err
			}
			return os.Symlink(outside, generation.Root)
		}},
	})
	if err == nil {
		t.Fatal("post-verification generation swap was accepted")
	}
	current, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || current.ID != oldID {
		t.Fatalf("CURRENT after switch race = %q err=%v want=%q", current.ID, resolveErr, oldID)
	}
	data, readErr := os.ReadFile(canary)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("outside canary changed: data=%q err=%v", data, readErr)
	}
}
