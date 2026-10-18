package devtools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// Operation is one stable development fixture mutation.
type Operation string

const (
	// OperationReset replaces a marked development fixture.
	OperationReset Operation = "reset"
	// OperationSeed builds the default deterministic fixture recipe.
	OperationSeed Operation = "seed"
	// OperationScenario builds a cataloged deterministic fixture recipe.
	OperationScenario Operation = "scenario"
)

// Request contains only explicit fixture inputs. Confirmation remains a CLI
// kernel responsibility and is deliberately absent from this handler.
type Request struct {
	DataDir   string
	Operation Operation
	Scenario  string
	Seed      int64
	DryRun    bool
	Progress  func(Progress)
	// Anchor dates the site-showcase scenario; zero means the current
	// minute. Every other scenario is dated from its seed and ignores it.
	Anchor time.Time

	hooks *operationHooks
}

// Plan is safe for human or JSON output and never exposes an absolute path or
// an instance secret.
type Plan struct {
	Operation     Operation `json:"operation"`
	Scenario      string    `json:"scenario"`
	Seed          int64     `json:"seed"`
	RecipeVersion string    `json:"recipe_version"`
	Counts        Counts    `json:"counts"`
	Bootstrap     bool      `json:"bootstrap"`
	DryRun        bool      `json:"dry_run"`
}

// Result reports the published logical fixture.
type Result struct {
	Plan                   Plan   `json:"plan"`
	Applied                bool   `json:"applied"`
	ExpectedDoctorCheckID  string `json:"expected_doctor_check_id,omitempty"`
	DoctorHealthy          bool   `json:"doctor_healthy"`
	ArchiveRoundTripPassed bool   `json:"archive_round_trip_passed,omitempty"`
	APIToken               string `json:"api_token,omitempty"`
	// Showcase lists what the site-showcase scenario built, under keys that
	// do not change between builds; every other scenario leaves it absent.
	Showcase *ShowcaseManifest `json:"showcase,omitempty"`
}

type operationHooks struct {
	AfterBootstrap         func() error
	BeforeCreate           func() error
	AfterCreate            func(runtimefs.Generation) error
	BeforeBuild            func(runtimefs.Generation) error
	BeforeSwitch           func(runtimefs.Generation) error
	AfterFinalSwitchVerify func(runtimefs.Generation) error
	AfterSwitch            func(runtimefs.Generation) error
	AfterRoundTripTarget   func(string) error
	AfterRoundTripCommit   func() error
	BeforeRoundTripCleanup func(string) error
}

// Execute validates one exact target, continuously owns all mutation locks,
// constructs an unreachable generation through application contracts, and
// publishes it with one atomic CURRENT switch. No failure path deletes a child.
func Execute(ctx context.Context, request Request) (result Result, resultErr error) {
	recipe, err := recipeFor(request)
	if err != nil {
		return Result{}, domain.NewError(domain.CodeInvalidArgument, err.Error(), map[string]any{"field": "scenario", "allowed": ScenarioNames()})
	}
	plan := Plan{
		Operation: request.Operation, Scenario: recipe.Name, Seed: request.Seed,
		RecipeVersion: RecipeVersion, Counts: recipe.Counts, DryRun: request.DryRun,
	}

	target, targetErr := OpenTarget(request.DataDir)
	if targetErr != nil {
		if request.Operation == OperationReset {
			return Result{}, targetErr
		}
		bootstrap, bootstrapErr := OpenBootstrapTarget(request.DataDir)
		freshBootstrap := bootstrapErr == nil
		if bootstrapErr != nil {
			bootstrap, bootstrapErr = OpenBootstrapRecoveryTarget(request.DataDir)
			if bootstrapErr != nil {
				return Result{}, classifyConcurrentBootstrap(request.DataDir, targetErr)
			}
		}
		plan.Bootstrap = true
		if request.DryRun {
			var err error
			if freshBootstrap {
				err = bootstrap.VerifyEmpty()
			} else {
				err = bootstrap.VerifyIdentity()
			}
			if err != nil {
				return Result{}, err
			}
			return Result{Plan: plan}, nil
		}
		if freshBootstrap {
			if err := bootstrap.VerifyEmpty(); err != nil {
				return Result{}, classifyConcurrentBootstrap(bootstrap.Root, err)
			}
		} else if err := bootstrap.VerifyIdentity(); err != nil {
			return Result{}, err
		}
		if freshBootstrap && request.hooks != nil && request.hooks.AfterBootstrap != nil {
			if err := request.hooks.AfterBootstrap(); err != nil {
				return Result{}, err
			}
			if err := bootstrap.VerifyEmpty(); err != nil {
				return Result{}, classifyConcurrentBootstrap(bootstrap.Root, err)
			}
		}
		bootstrapAuthority, err := bootstrap.OpenAuthority()
		if err != nil {
			return Result{}, err
		}
		_, markerCreated, err := runtimefs.InitializeDevelopmentRoot(ctx, bootstrap.Root, bootstrapAuthority)
		if err != nil {
			if freshBootstrap && ctx.Err() == nil {
				if entries, readErr := fs.ReadDir(bootstrapAuthority.FS(), "."); readErr == nil && len(entries) != 0 {
					_ = bootstrapAuthority.Close()
					return Result{}, domain.NewError(domain.CodeInstanceInUse, "another process initialized this development directory", nil)
				}
			}
			_ = bootstrapAuthority.Close()
			return Result{}, err
		}
		if err := bootstrapAuthority.Close(); err != nil {
			return Result{}, err
		}
		if freshBootstrap && !markerCreated {
			return Result{}, domain.NewError(domain.CodeInstanceInUse, "another process initialized this development directory", nil)
		}
		target, err = OpenTarget(bootstrap.Root)
		if err != nil {
			return Result{}, errors.Join(errors.New("development marker was published but could not be reopened; retry the same command without changing the directory"), err)
		}
	}

	runtimeState, held, authority, err := openCoordinatedTarget(ctx, target)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = held.Release() }()
	defer func() { _ = authority.Close() }()
	if request.DryRun {
		if err := target.Verify(); err != nil {
			return Result{}, err
		}
		return Result{Plan: plan}, nil
	}

	if err := runHook(request.hooks, func(hooks *operationHooks) func() error { return hooks.BeforeCreate }); err != nil {
		return Result{}, err
	}
	if err := target.Verify(); err != nil {
		return Result{}, err
	}
	signerStatus, err := auth.ReadStatusWithinRoot(authority)
	if err != nil {
		return Result{}, err
	}
	needsSigner := !signerStatus.Initialized
	generation, err := runtimeState.CreateDevelopmentGeneration(ctx, authority, held)
	if err != nil {
		return Result{}, err
	}
	staged, err := pinGeneration(target, generation)
	if err != nil {
		return Result{}, err
	}
	if request.hooks != nil && request.hooks.AfterCreate != nil {
		if err := request.hooks.AfterCreate(generation); err != nil {
			return Result{}, err
		}
		if err := staged.Verify(target); err != nil {
			return Result{}, err
		}
	}
	stagedAuthority := staged.authority(target)
	pinnedPaths, err := openPinnedRuntimePaths(authority, target, stagedAuthority)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = pinnedPaths.Close() }()

	stagedRuntime := &runtimefs.Runtime{
		Root: target.Root, Instance: target.Instance, Current: generation, Locks: runtimeState.Locks,
	}
	developmentAuthority, err := runtimefs.BindDevelopmentAuthority(stagedRuntime, authority, pinnedPaths.generationRoot, pinnedPaths.databasePath, held)
	if err != nil {
		return Result{}, err
	}
	database, err := sqlitestore.OpenDevelopmentPinned(ctx, stagedRuntime, held, developmentAuthority, sqlitestore.Options{})
	if err != nil {
		return Result{}, err
	}
	databaseOpen := true
	defer func() {
		if databaseOpen {
			resultErr = errors.Join(resultErr, database.Close())
		}
	}()
	if request.hooks != nil && request.hooks.BeforeBuild != nil {
		if err := request.hooks.BeforeBuild(generation); err != nil {
			return Result{}, err
		}
	}
	if err := staged.Verify(target); err != nil {
		return Result{}, err
	}
	builder, err := newFixtureBuilderWithinLocks(target, stagedRuntime, database, developmentAuthority, held, staged, recipe, request.Seed, request.Anchor, request.Progress)
	if err != nil {
		return Result{}, err
	}
	if _, err := builder.build(ctx); err != nil {
		return Result{}, err
	}
	showcase := builder.manifest
	if recipe.ExpectedDoctorCheckID == "fts_consistency" {
		if err := sqlitestore.CorruptDevelopmentFTS(ctx, database, target.Root, held); err != nil {
			return Result{}, err
		}
	}
	if err := staged.Verify(target); err != nil {
		return Result{}, err
	}
	if err := database.Close(); err != nil {
		return Result{}, err
	}
	databaseOpen = false
	if request.hooks != nil && request.hooks.BeforeSwitch != nil {
		if err := request.hooks.BeforeSwitch(generation); err != nil {
			return Result{}, err
		}
	}
	if err := staged.Verify(target); err != nil {
		return Result{}, err
	}
	if request.hooks != nil && request.hooks.AfterFinalSwitchVerify != nil {
		if err := request.hooks.AfterFinalSwitchVerify(generation); err != nil {
			return Result{}, err
		}
	}
	switchErr := runtimeState.SwitchCurrentDevelopment(ctx, developmentAuthority.GenerationAuthority(), authority, held)
	if switchErr != nil {
		return Result{}, rollbackIfPublished(runtimeState, authority, held, targetGenerationAuthority(target), generation.ID, switchErr)
	}
	runtimeState.Current = generation
	postSwitchErr := func() error {
		if request.hooks != nil && request.hooks.AfterSwitch != nil {
			if err := request.hooks.AfterSwitch(generation); err != nil {
				return err
			}
		}
		published, err := runtimeState.ResolveCurrentDevelopment(authority, target.rootInfo)
		if err != nil || published.ID != generation.ID || developmentAuthority.Validate(runtimeState, held) != nil {
			return errors.Join(unsafeTarget("the published development authority could not be reverified"), err)
		}
		publishedDatabase, err := sqlitestore.OpenDevelopmentPinned(ctx, stagedRuntime, held, developmentAuthority, sqlitestore.Options{})
		if err != nil {
			return err
		}
		defer func() { _ = publishedDatabase.Close() }()
		diagnostic, err := doctor.New(doctor.Options{LockedSnapshot: &doctor.LockedSnapshot{
			Runtime: runtimeState, Database: publishedDatabase, Held: held, Authority: developmentAuthority,
		}})
		if err != nil {
			return err
		}
		report, err := diagnostic.Run(ctx, doctor.ModeDeep)
		if err != nil {
			return err
		}
		if _, err = validateDoctorHandoff(report, recipe.ExpectedDoctorCheckID, needsSigner); err != nil {
			return err
		}
		if recipe.ArchiveRoundTrip {
			if err := performArchiveRoundTrip(ctx, runtimeState, publishedDatabase, developmentAuthority, held, recipe, request.Seed, request.hooks); err != nil {
				return err
			}
		}
		return nil
	}()
	if postSwitchErr != nil {
		return Result{}, rollbackIfPublished(runtimeState, authority, held, targetGenerationAuthority(target), generation.ID, postSwitchErr)
	}
	healthy := recipe.ExpectedDoctorCheckID == ""
	apiToken, err := issueDevelopmentAccountToken(context.WithoutCancel(ctx), runtimeState, developmentAuthority, held)
	if err != nil {
		return Result{}, rollbackIfPublished(runtimeState, authority, held, targetGenerationAuthority(target), generation.ID, err)
	}
	if needsSigner {
		if _, err := auth.InitializeDevelopmentWithinLocks(context.WithoutCancel(ctx), runtimeState, developmentAuthority, held); err != nil {
			return Result{}, rollbackIfPublished(runtimeState, authority, held, targetGenerationAuthority(target), generation.ID, err)
		}
	}

	return Result{
		Plan: plan, Applied: true, ExpectedDoctorCheckID: recipe.ExpectedDoctorCheckID,
		DoctorHealthy: healthy, ArchiveRoundTripPassed: recipe.ArchiveRoundTrip, APIToken: apiToken,
		Showcase: showcase,
	}, nil
}

func classifyConcurrentBootstrap(root string, primary error) error {
	instance, err := runtimefs.ReadInstance(root)
	if err == nil && instance.Environment == runtimefs.EnvironmentDevelopment {
		return domain.NewError(domain.CodeInstanceInUse, "another process initialized this development directory", nil)
	}
	entries, readErr := os.ReadDir(root)
	if readErr == nil && len(entries) != 0 {
		controlled := true
		for _, entry := range entries {
			name := entry.Name()
			switch name {
			case ".bootstrap.lock", "instance.json", "CURRENT", "generations", "cache", "backups", "staging", "tmp", "locks":
			default:
				controlled = controlled && (strings.HasPrefix(name, ".instance.json.") || strings.HasPrefix(name, ".CURRENT."))
			}
		}
		if controlled {
			return domain.NewError(domain.CodeInstanceInUse, "another process initialized this development directory", nil)
		}
	}
	return primary
}

func rollbackIfPublished(runtimeState *runtimefs.Runtime, authority *os.Root, held *runtimefs.LeaseSet, old runtimefs.DevelopmentGenerationAuthority, newID string, operationErr error) error {
	current, resolveErr := runtimeState.ResolveCurrentDevelopment(authority, old.InstanceRoot)
	if resolveErr == nil && current.ID == old.ID {
		return operationErr
	}
	if resolveErr != nil || current.ID != newID {
		return errors.Join(domain.NewError(domain.CodeDataIntegrityFailed, "development publish failed and CURRENT needs inspection", map[string]any{"needs_attention": true}), operationErr, resolveErr)
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
	defer cancel()
	rollbackErr := runtimeState.SwitchCurrentDevelopment(rollbackCtx, old, authority, held)
	if rollbackErr == nil {
		rolledBack, verifyErr := runtimeState.ResolveCurrentDevelopment(authority, old.InstanceRoot)
		if verifyErr != nil || rolledBack.ID != old.ID {
			rollbackErr = errors.Join(errors.New("rollback CURRENT verification failed"), verifyErr)
		}
	}
	if rollbackErr != nil {
		return errors.Join(domain.NewError(domain.CodeDataIntegrityFailed, "development publish failed and rollback needs attention", map[string]any{"needs_attention": true}), operationErr, rollbackErr)
	}
	return operationErr
}

func validateDoctorHandoff(report doctor.Report, expected string, allowMissingSigner bool) (bool, error) {
	failures := make([]doctor.Check, 0, report.Counts.Fail)
	for _, check := range report.Checks {
		if check.Status != doctor.StatusFail {
			continue
		}
		if allowMissingSigner && check.ID == doctor.CheckSecretPermissions {
			continue
		}
		failures = append(failures, check)
	}
	if expected == "" {
		if len(failures) != 0 {
			ids := make([]string, len(failures))
			for index := range failures {
				ids[index] = failures[index].ID
			}
			return false, domain.NewError(domain.CodeDataIntegrityFailed, "the generated scenario failed its doctor handoff", map[string]any{"failure": report.Failure(), "check_ids": ids})
		}
		return true, nil
	}
	if len(failures) != 1 || failures[0].ID != expected || failures[0].Severity != doctor.SeverityError {
		return false, domain.NewError(domain.CodeDataIntegrityFailed, "the corruption scenario produced an unexpected doctor finding", map[string]any{"expected_check_id": expected, "failure_count": len(failures)})
	}
	return false, nil
}

func recipeFor(request Request) (Scenario, error) {
	switch request.Operation {
	case OperationReset:
		if request.Scenario != "" {
			return Scenario{}, errors.New("reset does not accept a scenario")
		}
		return LookupScenario("timeline-empty")
	case OperationSeed:
		if request.Scenario != "" {
			return Scenario{}, errors.New("seed does not accept a scenario")
		}
		return DefaultSeedScenario(), nil
	case OperationScenario:
		return LookupScenario(request.Scenario)
	default:
		return Scenario{}, fmt.Errorf("unsupported development operation %q", request.Operation)
	}
}

func runHook(hooks *operationHooks, selectHook func(*operationHooks) func() error) error {
	if hooks == nil {
		return nil
	}
	hook := selectHook(hooks)
	if hook == nil {
		return nil
	}
	return hook()
}

// issueDevelopmentAccountToken creates a normal credential in the newly seeded
// generation. Resetting the generation never preserves a previous API token.
func issueDevelopmentAccountToken(ctx context.Context, runtimeState *runtimefs.Runtime, authority *runtimefs.DevelopmentAuthority, held *runtimefs.LeaseSet) (value string, resultErr error) {
	database, err := sqlitestore.OpenDevelopmentPinned(ctx, runtimeState, held, authority, sqlitestore.Options{})
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	repository, err := sqlitestore.NewDevelopmentAccountRepository(database, runtimeState.Root, held)
	if err != nil {
		return "", err
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		return "", err
	}
	account, err := accounts.ProfileAccount(ctx)
	if err != nil {
		return "", err
	}
	issued, err := accounts.CreateAPIToken(ctx, account.ID, "development")
	return issued.Value, err
}
