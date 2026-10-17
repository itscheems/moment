package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/transport/searchdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerSearch(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"search", "query"},
		ArgsUsage: "[text]",
		Short:     "Search published Moment history",
		Long:      "Search normalized published post text with stable relevance pagination and complete site-timezone year counts through the selected transport.",
		Inputs:    "The search text, either as the single argument or as --query; optional inclusive --from, exclusive --to, opaque --cursor, and --limit. Dates use the persisted site IANA timezone.",
		Output:    "Canonical post hits with plain safe snippets, complete year_counts, and a nullable next cursor in metadata.",
		Errors:    "Empty, overlong, malformed, or cursor-mismatched input returns INVALID_ARGUMENT; unavailable derived state returns a stable integrity error.",
		Example: "moment search query '旧城 雨' --data-dir ./moment-data --json\n" +
			"moment search query --query '旧城 雨' --from 2025-01-01 --data-dir ./moment-data --json",
		Args:       cobra.MaximumNArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("query", "", "search text containing Chinese, English, numbers, or a phrase")
			flags.String("cursor", "", "opaque continuation cursor from the previous page")
			flags.Int("limit", 0, "page size from 1 to 100 (default 20)")
			flags.String("from", "", "inclusive local date in YYYY-MM-DD format")
			flags.String("to", "", "exclusive local date in YYYY-MM-DD format")
		},
		Run: runSearchQuery,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"search", "rebuild"},
		Short:      "Repair the derived search index",
		Long:       "Diagnose or atomically rebuild versioned content_text and FTS rows from authoritative post Markdown without changing post identity, Markdown, or timestamps.",
		Inputs:     "A local data directory. Use --dry-run for read-only diagnostics; API mode is rejected as MODE_UNSUPPORTED.",
		Output:     "Normalizer versions plus total, ready, drifted, orphan, and rebuilt counts.",
		Errors:     "Authority corruption returns DATA_INTEGRITY_FAILED; timeout or cancellation rolls back the complete rebuild.",
		Safety:     "Only derived content_text and FTS rows are replaced atomically; actual work requires confirmation or --yes.",
		Example:    "moment search rebuild --data-dir ./moment-data --dry-run --json",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Rebuild the derived search index now?"},
		Run:        runSearchRebuild,
	})
}

func runSearchQuery(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	query, err := input.String("query")
	if err != nil {
		return Result{}, err
	}
	// The search text is the one thing this command is about, so it reads as
	// an argument the way every other single-subject command does. The flag
	// stays for symmetry with the rest of the flag set, and naming both is a
	// mistake worth pointing at rather than silently resolving.
	if positional := input.Args(); len(positional) == 1 {
		if input.Changed("query") {
			return Result{}, invalidArgument("search text was given twice", "pass the text as the argument or as --query, not both")
		}
		query = positional[0]
	} else if !input.Changed("query") {
		return Result{}, invalidArgument("search text is required", "pass it as the argument: moment search query '旧城 雨'")
	}
	searchInput := app.SearchInput{Query: query}
	if searchInput.Cursor, err = input.String("cursor"); err != nil {
		return Result{}, err
	}
	if input.Changed("cursor") && searchInput.Cursor == "" {
		return Result{}, invalidArgument("--cursor cannot be empty", "omit --cursor or provide the previous next_cursor value")
	}
	if searchInput.Limit, err = input.Int("limit"); err != nil {
		return Result{}, err
	}
	if input.Changed("limit") && searchInput.Limit == 0 {
		return Result{}, invalidArgument("--limit must be between 1 and 100", "use a positive page size such as --limit 20")
	}
	if searchInput.FromDate, err = input.String("from"); err != nil {
		return Result{}, err
	}
	if input.Changed("from") && searchInput.FromDate == "" {
		return Result{}, invalidArgument("--from cannot be empty", "omit --from or provide an inclusive YYYY-MM-DD date")
	}
	if searchInput.ToDate, err = input.String("to"); err != nil {
		return Result{}, err
	}
	if input.Changed("to") && searchInput.ToDate == "" {
		return Result{}, invalidArgument("--to cannot be empty", "omit --to or provide an exclusive YYYY-MM-DD date")
	}

	var data searchdto.SearchData
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		service, database, err := openDirectSearch(ctx, invocation)
		if err != nil {
			return Result{}, searchCLIError(err)
		}
		defer func() { _ = database.Close() }()
		searchInput.Viewer = app.OwnerViewer()
		page, err := service.Search(ctx, searchInput)
		if err != nil {
			return Result{}, searchCLIError(err)
		}
		data = searchdto.FromSearch(page)
		meta = map[string]any{"next_cursor": searchdto.NextCursor(page)}
	case ModeAPI:
		values := url.Values{"q": []string{searchInput.Query}}
		if searchInput.Cursor != "" {
			values.Set("cursor", searchInput.Cursor)
		}
		if searchInput.Limit != 0 {
			values.Set("limit", strconv.Itoa(searchInput.Limit))
		}
		if searchInput.FromDate != "" {
			values.Set("from", searchInput.FromDate)
		}
		if searchInput.ToDate != "" {
			values.Set("to", searchInput.ToDate)
		}
		var status int
		meta, status, err = postAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/search", values, nil, "", false, &data)
		if err != nil {
			return Result{}, searchCLIError(err)
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		if _, err := postCursorFromMeta(meta); err != nil {
			return Result{}, err
		}
	default:
		return Result{}, errors.New("unsupported search transport")
	}
	if err := searchdto.ValidateSearch(data); err != nil {
		return Result{}, invalidAPIResponse()
	}
	return Result{Data: data, Meta: meta, Text: searchText(data, meta)}, nil
}

func runSearchRebuild(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	runtimeState, maintenance, err := runtimefs.OpenForWriteContext(ctx, target.DataDirectory())
	if err != nil {
		return Result{}, runtimeError(err)
	}
	defer func() { _ = maintenance.Release() }()
	if invocation.Safety().DryRun {
		return runSearchRebuildDryRun(ctx, runtimeState)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return Result{}, postDatabaseError(err)
	}
	defer func() { _ = database.Close() }()
	repository, err := sqlitestore.NewSearchRepositoryWithinMaintenance(database, maintenance, runtimeState.Locks)
	if err != nil {
		return Result{}, err
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		return Result{}, err
	}
	result, err := service.Rebuild(ctx, invocation.Safety().DryRun)
	if err != nil {
		return Result{}, searchCLIError(err)
	}
	verb := "Search index inspected"
	if !result.DryRun {
		verb = "Search index rebuilt"
	}
	return Result{Data: result, Text: fmt.Sprintf("%s: %d total, %d ready, %d drifted, %d orphan rows, %d rebuilt", verb, result.Total, result.Ready, result.Drifted, result.OrphanIndexRows, result.Rebuilt)}, nil
}

func runSearchRebuildDryRun(ctx context.Context, runtimeState *runtimefs.Runtime) (Result, error) {
	database, err := sqlitestore.OpenSearchReadOnly(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		if errors.Is(err, sqlitestore.ErrSearchSchemaUpgradeRequired) {
			return Result{}, NewCommandError(
				"SEARCH_SCHEMA_UPGRADE_REQUIRED",
				"the search schema must be upgraded before a read-only rebuild inspection",
				"start the updated Moment server once or run 'moment search rebuild --yes', then retry --dry-run",
				ExitIntegrity,
			).WithCause(err)
		}
		return Result{}, postDatabaseError(err)
	}
	defer func() { _ = database.Close() }()
	repository, err := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		return Result{}, err
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		return Result{}, err
	}
	result, err := service.Rebuild(ctx, true)
	if err != nil {
		return Result{}, searchCLIError(err)
	}
	return Result{Data: result, Text: fmt.Sprintf("Search index inspected: %d total, %d ready, %d drifted, %d orphan rows, %d rebuilt", result.Total, result.Ready, result.Drifted, result.OrphanIndexRows, result.Rebuilt)}, nil
}

func openDirectSearch(ctx context.Context, invocation Invocation) (*app.SearchService, *sqlitestore.Database, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, nil, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, runtimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return nil, nil, postDatabaseError(err)
	}
	repository, err := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	return service, database, nil
}

func searchText(data searchdto.SearchData, meta map[string]any) string {
	if len(data.Items) == 0 {
		return "No matching posts found"
	}
	lines := make([]string, 0, len(data.Items)+1)
	for _, item := range data.Items {
		lines = append(lines, fmt.Sprintf("%s  %s  %s", item.ID, item.PublishedAt.Format("2006-01-02T15:04:05Z"), item.Snippet))
	}
	if cursor, _ := postCursorFromMeta(meta); cursor != nil {
		lines = append(lines, "Next cursor: "+*cursor)
	}
	return strings.Join(lines, "\n")
}

func searchCLIError(err error) error {
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return err
	}
	return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
}
