package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/moment/moment/internal/domain"
)

const (
	// MaxSearchQueryCodePoints bounds public search work before normalization.
	MaxSearchQueryCodePoints = 256
	searchRebuildBatchSize   = 256
)

// SearchMode selects one fixed repository query. User input never selects SQL.
type SearchMode uint8

const (
	// SearchModeLike selects the escaped short-query fallback.
	SearchModeLike SearchMode = iota + 1
	// SearchModeFTS selects the quoted FTS5 trigram query.
	SearchModeFTS
)

// SearchRepositoryQuery is the prepared repository contract.
type SearchRepositoryQuery struct {
	Mode       SearchMode
	QueryValue string
	FromMS     *int64
	ToMS       *int64
	Position   *SearchPosition
	Viewer     Viewer
}

// SearchRecord is one matched authoritative Entry plus derived sort/search
// values used only inside the application boundary.
type SearchRecord struct {
	Entry       domain.Entry
	Media       []MediaUsageProjection
	ContentText string
	Rank        float64
}

// SearchSource reports enough derived state to diagnose one entry without
// trusting either content_text or FTS as authority.
type SearchSource struct {
	ID domain.ID
	// Kind decides which of the two bodies below carries the text. A moment
	// has Markdown; an article has a title and a structured body.
	Kind                domain.EntryKind
	ContentMD           *string
	ArticleTitle        string
	ArticleBodyJSON     string
	ContentText         string
	MapCard             *domain.MapCard
	LinkCardText        string
	FTSCopies           int
	FTSStoredTextCopies int
}

// DerivedSearchText is the single authority for a moment's indexed text. Both
// the write path and rebuild call it, so the two can never drift and leave the
// doctor's FTS consistency check reporting phantom damage.
func DerivedSearchText(contentMD string, cards []domain.LinkCard, maps ...*domain.MapCard) (string, error) {
	text := domain.LinkCardSearchText(cards)
	if len(maps) > 0 && maps[0] != nil {
		text += "\n" + maps[0].Text()
	}
	return derivedSearchText(contentMD, text)
}

// DerivedArticleSearchText is the same authority for an article. Its title is
// part of it because a title is how anybody looks for an article again.
func DerivedArticleSearchText(title string, body domain.ArticleDocument) string {
	return domain.ArticleSearchText(title, body)
}

// sourceSearchText derives what one stored row's indexed text should be,
// whichever body shape it has. The doctor and the rebuild both go through here
// so they cannot disagree about what "correct" means.
func sourceSearchText(source SearchSource) (string, error) {
	if source.Kind == domain.KindArticle {
		body, err := domain.ParseArticleDocument([]byte(source.ArticleBodyJSON))
		if err != nil {
			return "", err
		}
		return DerivedArticleSearchText(source.ArticleTitle, body), nil
	}
	if source.ContentMD == nil {
		return "", domain.NewError(domain.CodeDataIntegrityFailed, "a moment was read without its body", nil)
	}
	text := source.LinkCardText
	if source.MapCard != nil {
		text += "\n" + source.MapCard.Text()
	}
	return derivedSearchText(*source.ContentMD, text)
}

func derivedSearchText(contentMD, cardText string) (string, error) {
	normalized, err := domain.NormalizeContentV2(contentMD)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cardText) == "" {
		return normalized, nil
	}
	normalizedCards, err := domain.NormalizeContentV2(cardText)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return normalizedCards, nil
	}
	return normalized + " " + normalizedCards, nil
}

// SearchStore is valid only for its transaction callback.
type SearchStore interface {
	QuoteReadStore
	ContentAuthorStore
	Timezone(context.Context) (string, error)
	SearchEntries(context.Context, SearchRepositoryQuery, int) ([]SearchRecord, error)
	CommentCounts(context.Context, []domain.ID) (map[domain.ID]int, error)
	EntryMediaUsages(context.Context, domain.ID) ([]MediaUsageProjection, error)
	EntryLocation(context.Context, domain.ID) (*domain.Location, error)
	EntryLinkCards(context.Context, domain.ID) ([]LinkCardProjection, error)
	ArticleCards(context.Context, []domain.ID) (map[domain.ID]ArticleCard, error)
	SearchPublishedMillis(context.Context, SearchRepositoryQuery) ([]int64, error)
	ArchivePublishedMillis(context.Context, Viewer, *int64, *int64) ([]int64, error)
	SearchNormalizerVersion(context.Context) (int, error)
	SearchRevision(context.Context) (int64, error)
	SearchOrphanCount(context.Context) (int, error)
	SearchSources(context.Context, domain.ID, int) ([]SearchSource, error)
	ClearSearchIndex(context.Context) error
	UpdateSearchDerived(context.Context, domain.ID, string) error
	SetSearchNormalizerVersion(context.Context, int) error
	AdvanceSearchRevision(context.Context) (int64, error)
}

// SearchTransactions supplies snapshot reads and atomic derived rebuilds.
type SearchTransactions interface {
	Read(context.Context, func(SearchStore) error) error
	Write(context.Context, func(SearchStore) error) error
}

// SearchInput is one public search request before site-timezone interpretation.
type SearchInput struct {
	Query    string
	Cursor   string
	Limit    int
	FromDate string
	ToDate   string
	// Viewer bounds both the page and the year counts; the zero value is a
	// visitor.
	Viewer Viewer
}

// SearchHit combines a canonical post with a safe plain-text snippet.
type SearchHit struct {
	Post    Post   `json:"post"`
	Snippet string `json:"snippet"`
}

// YearCount is one complete-match-set calendar aggregate.
type YearCount struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// SearchPage contains one cursor page plus complete matching year counts.
type SearchPage struct {
	Items      []SearchHit `json:"items"`
	YearCounts []YearCount `json:"year_counts"`
	NextCursor string      `json:"-"`
}

// ArchiveMonthsInput bounds archive aggregates by optional local dates.
type ArchiveMonthsInput struct {
	FromDate string
	ToDate   string
	Viewer   Viewer
}

// ArchiveMonthCount is one non-empty site-timezone month.
type ArchiveMonthCount struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Count int `json:"count"`
}

// ArchiveMonths lists non-empty months in reverse calendar order.
type ArchiveMonths struct {
	Items []ArchiveMonthCount `json:"items"`
}

// ArchiveDayCount is one non-empty day in a requested month.
type ArchiveDayCount struct {
	Day   int `json:"day"`
	Count int `json:"count"`
}

// ArchiveMonth contains site-timezone day counts for one month.
type ArchiveMonth struct {
	Month string            `json:"month"`
	Days  []ArchiveDayCount `json:"days"`
}

// SearchRebuildResult reports derived-index diagnostics and repair work.
type SearchRebuildResult struct {
	DryRun            bool  `json:"dry_run"`
	NormalizerVersion int   `json:"normalizer_version"`
	StoredVersion     int   `json:"stored_version"`
	Total             int   `json:"total"`
	Ready             int   `json:"ready"`
	Drifted           int   `json:"drifted"`
	OrphanIndexRows   int   `json:"orphan_index_rows"`
	Rebuilt           int   `json:"rebuilt"`
	Revision          int64 `json:"revision"`
}

// SearchDiagnostic is the bounded, read-only search authority view used by
// doctor. It never returns source Markdown or normalized content.
type SearchDiagnostic struct {
	Total           int   `json:"total"`
	Ready           int   `json:"ready"`
	Drifted         int   `json:"drifted"`
	OrphanIndexRows int   `json:"orphan_index_rows"`
	StoredVersion   int   `json:"stored_version"`
	ExpectedVersion int   `json:"expected_version"`
	Revision        int64 `json:"revision"`
	Bounded         bool  `json:"bounded"`
}

// SearchService owns normalized search, calendar aggregates, and rebuilding.
type SearchService struct{ transactions SearchTransactions }

// NewSearchService constructs the application service over transaction ports.
func NewSearchService(transactions SearchTransactions) (*SearchService, error) {
	if transactions == nil {
		return nil, errors.New("search transactions are required")
	}
	return &SearchService{transactions: transactions}, nil
}

// Search returns one deterministic relevance page and year counts for the
// complete matching set from the same read snapshot.
func (service *SearchService) Search(ctx context.Context, input SearchInput) (SearchPage, error) {
	if err := contextError(ctx); err != nil {
		return SearchPage{}, err
	}
	prepared, normalized, err := prepareSearchQuery(input.Query)
	if err != nil {
		return SearchPage{}, err
	}
	limit, err := normalizePostLimit(input.Limit)
	if err != nil {
		return SearchPage{}, err
	}

	var records []SearchRecord
	var instants []int64
	var location *time.Location
	var timezone string
	var revision int64
	var fromMS, toMS *int64
	commentCounts := make(map[domain.ID]int)
	authors := make(map[string]CommentAuthorIdentity)
	articleCards := make(map[domain.ID]ArticleCard)
	quoted := make(map[domain.ID]*QuotedPost)
	locations := make(map[domain.ID]*domain.Location)
	linkCards := make(map[domain.ID][]LinkCardProjection)
	err = service.transactions.Read(ctx, func(store SearchStore) error {
		var storeErr error
		version, storeErr := store.SearchNormalizerVersion(ctx)
		if storeErr != nil {
			return storeErr
		}
		if version != domain.ContentNormalizerVersion {
			return searchDataIntegrity("normalizer version is not current")
		}
		revision, storeErr = store.SearchRevision(ctx)
		if storeErr != nil {
			return storeErr
		}
		if revision < 1 {
			return searchDataIntegrity("revision is invalid")
		}
		timezone, storeErr = store.Timezone(ctx)
		if storeErr != nil {
			return storeErr
		}
		location, fromMS, toMS, storeErr = searchDateContext(input.FromDate, input.ToDate, timezone)
		if storeErr != nil {
			return storeErr
		}
		signature := searchSignature(normalized, timezone, fromMS, toMS)
		position, cursorErr := decodeSearchCursor(input.Cursor, signature, revision)
		if cursorErr != nil {
			return cursorErr
		}
		prepared.FromMS, prepared.ToMS, prepared.Position, prepared.Viewer = fromMS, toMS, position, input.Viewer
		records, storeErr = store.SearchEntries(ctx, prepared, limit+1)
		if storeErr != nil {
			return storeErr
		}
		authorEntries := make([]domain.Entry, 0, len(records))
		for _, record := range records {
			authorEntries = append(authorEntries, record.Entry)
		}
		authors, storeErr = readContentAuthors(ctx, store, authorEntries)
		if storeErr != nil {
			return storeErr
		}
		for index := range records {
			quoted[records[index].Entry.ID], storeErr = loadEntryQuote(ctx, store, input.Viewer, records[index].Entry.ID)
			if storeErr != nil {
				return storeErr
			}
			records[index].Media, storeErr = store.EntryMediaUsages(ctx, records[index].Entry.ID)
			if storeErr != nil {
				return storeErr
			}
			locations[records[index].Entry.ID], storeErr = store.EntryLocation(ctx, records[index].Entry.ID)
			if storeErr != nil {
				return storeErr
			}
			linkCards[records[index].Entry.ID], storeErr = store.EntryLinkCards(ctx, records[index].Entry.ID)
			if storeErr != nil {
				return storeErr
			}
		}
		ids := make([]domain.ID, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.Entry.ID)
		}
		commentCounts, storeErr = store.CommentCounts(ctx, ids)
		if storeErr != nil {
			return storeErr
		}
		// A result is a card, like a timeline row: the body stays behind.
		if articleCards, storeErr = store.ArticleCards(ctx, ids); storeErr != nil {
			return storeErr
		}
		countQuery := prepared
		countQuery.Position = nil
		instants, storeErr = store.SearchPublishedMillis(ctx, countQuery)
		return storeErr
	})
	if err != nil {
		return SearchPage{}, normalizeSearchRepositoryError(err)
	}

	hasNext := len(records) > limit
	if hasNext {
		records = records[:limit]
	}
	page := SearchPage{
		Items:      make([]SearchHit, 0, len(records)),
		YearCounts: aggregateYears(instants, location),
	}
	for _, record := range records {
		post, renderErr := renderPost(record.Entry, articleView{Card: articleCardFor(articleCards, record.Entry)}, record.Media, locations[record.Entry.ID], linkCards[record.Entry.ID], commentCounts[record.Entry.ID], quoted[record.Entry.ID] != nil)
		if renderErr != nil {
			return SearchPage{}, renderErr
		}
		post.Quoted = quoted[record.Entry.ID]
		post.Author = contentAuthor(authors, record.Entry.AuthorUserID)
		page.Items = append(page.Items, SearchHit{Post: post, Snippet: searchSnippet(record.ContentText, normalized)})
	}
	if hasNext {
		last := records[len(records)-1]
		page.NextCursor = encodeSearchCursor(SearchPosition{
			Rank: last.Rank, PublishedAtMS: domain.TimeToMillis(last.Entry.PublishedAt), ID: last.Entry.ID,
		}, searchSignature(normalized, timezone, fromMS, toMS), revision)
	}
	return page, nil
}

// ArchiveMonths returns non-empty site-timezone month counts.
func (service *SearchService) ArchiveMonths(ctx context.Context, input ArchiveMonthsInput) (ArchiveMonths, error) {
	if input.Viewer.UserID() == "" {
		return ArchiveMonths{}, domain.NewError(domain.CodeUnauthorized, "sign in to read your archive", nil)
	}
	if err := contextError(ctx); err != nil {
		return ArchiveMonths{}, err
	}
	var instants []int64
	var location *time.Location
	err := service.transactions.Read(ctx, func(store SearchStore) error {
		timezone, err := store.Timezone(ctx)
		if err != nil {
			return err
		}
		var fromMS, toMS *int64
		location, fromMS, toMS, err = searchDateContext(input.FromDate, input.ToDate, timezone)
		if err != nil {
			return err
		}
		instants, err = store.ArchivePublishedMillis(ctx, input.Viewer, fromMS, toMS)
		return err
	})
	if err != nil {
		return ArchiveMonths{}, normalizeSearchRepositoryError(err)
	}
	return ArchiveMonths{Items: aggregateMonths(instants, location)}, nil
}

// ArchiveMonth returns non-empty day counts for one site-timezone month, as
// the viewer sees them.
func (service *SearchService) ArchiveMonth(ctx context.Context, month string, viewer Viewer) (ArchiveMonth, error) {
	if viewer.UserID() == "" {
		return ArchiveMonth{}, domain.NewError(domain.CodeUnauthorized, "sign in to read your archive", nil)
	}
	if err := contextError(ctx); err != nil {
		return ArchiveMonth{}, err
	}
	if len(month) != len("2006-01") {
		return ArchiveMonth{}, invalidArchiveMonth()
	}
	var instants []int64
	var location *time.Location
	err := service.transactions.Read(ctx, func(store SearchStore) error {
		timezone, err := store.Timezone(ctx)
		if err != nil {
			return err
		}
		if err := validateTimezone(timezone); err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is invalid", map[string]any{"field": "timezone"})
		}
		location, err = time.LoadLocation(timezone)
		if err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is unavailable", map[string]any{"field": "timezone"})
		}
		start, err := time.ParseInLocation("2006-01", month, location)
		if err != nil || start.Format("2006-01") != month {
			return invalidArchiveMonth()
		}
		fromMS := domain.TimeToMillis(start)
		toMS := domain.TimeToMillis(start.AddDate(0, 1, 0))
		instants, err = store.ArchivePublishedMillis(ctx, viewer, &fromMS, &toMS)
		return err
	})
	if err != nil {
		return ArchiveMonth{}, normalizeSearchRepositoryError(err)
	}
	return ArchiveMonth{Month: month, Days: aggregateDays(instants, location)}, nil
}

// Rebuild repairs only content_text and FTS inside one transaction. Dry-run
// uses a read-only transaction and both modes report deterministic counts.
func (service *SearchService) Rebuild(ctx context.Context, dryRun bool) (SearchRebuildResult, error) {
	if err := contextError(ctx); err != nil {
		return SearchRebuildResult{}, err
	}
	result := SearchRebuildResult{DryRun: dryRun, NormalizerVersion: domain.ContentNormalizerVersion}
	operation := func(store SearchStore) error {
		var err error
		result.StoredVersion, err = store.SearchNormalizerVersion(ctx)
		if err != nil {
			return err
		}
		if result.StoredVersion > domain.ContentNormalizerVersion {
			return searchDataIntegrity("normalizer version is newer than this binary")
		}
		result.Revision, err = store.SearchRevision(ctx)
		if err != nil {
			return err
		}
		if result.Revision < 1 {
			return searchDataIntegrity("revision is invalid")
		}
		result.OrphanIndexRows, err = store.SearchOrphanCount(ctx)
		if err != nil {
			return err
		}
		if err := service.scanDerived(ctx, store, &result); err != nil {
			return err
		}
		needsRebuild := result.StoredVersion != domain.ContentNormalizerVersion || result.Drifted > 0 || result.OrphanIndexRows > 0
		if dryRun || !needsRebuild {
			return nil
		}
		if err := store.ClearSearchIndex(ctx); err != nil {
			return err
		}
		if err := service.writeAllDerived(ctx, store); err != nil {
			return err
		}
		if err := store.SetSearchNormalizerVersion(ctx, domain.ContentNormalizerVersion); err != nil {
			return err
		}
		result.Revision, err = store.AdvanceSearchRevision(ctx)
		if err != nil {
			return err
		}
		result.Rebuilt = result.Total
		return nil
	}
	var err error
	if dryRun {
		err = service.transactions.Read(ctx, operation)
	} else {
		err = service.transactions.Write(ctx, operation)
	}
	if err != nil {
		return SearchRebuildResult{}, normalizeSearchRepositoryError(err)
	}
	return result, nil
}

// DiagnoseDerived scans at most maxEntries authoritative rows through the
// same normalizer and repository contract as rebuild, but cannot write.
func (service *SearchService) DiagnoseDerived(ctx context.Context, maxEntries int) (SearchDiagnostic, error) {
	if maxEntries < 1 {
		return SearchDiagnostic{}, domain.NewError(domain.CodeInvalidArgument, "search diagnostic limit must be positive", nil)
	}
	result := SearchDiagnostic{ExpectedVersion: domain.ContentNormalizerVersion}
	err := service.transactions.Read(ctx, func(store SearchStore) error {
		var err error
		result.StoredVersion, err = store.SearchNormalizerVersion(ctx)
		if err != nil {
			return err
		}
		result.Revision, err = store.SearchRevision(ctx)
		if err != nil {
			return err
		}
		result.OrphanIndexRows, err = store.SearchOrphanCount(ctx)
		if err != nil {
			return err
		}
		var after domain.ID
		for result.Total < maxEntries {
			if err := ctx.Err(); err != nil {
				return err
			}
			remaining := maxEntries - result.Total
			limit := min(searchRebuildBatchSize, remaining+1)
			sources, err := store.SearchSources(ctx, after, limit)
			if err != nil {
				return err
			}
			for _, source := range sources {
				if result.Total == maxEntries {
					result.Bounded = true
					return nil
				}
				expected, err := sourceSearchText(source)
				if err != nil {
					return storedEntryError(source.ID)
				}
				result.Total++
				if source.ContentText == expected && source.FTSCopies == 1 && source.FTSStoredTextCopies == 1 && result.StoredVersion == domain.ContentNormalizerVersion {
					result.Ready++
				} else {
					result.Drifted++
				}
				after = source.ID
			}
			if len(sources) < limit {
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return SearchDiagnostic{}, normalizeSearchRepositoryError(err)
	}
	return result, nil
}

func (service *SearchService) scanDerived(ctx context.Context, store SearchStore, result *SearchRebuildResult) error {
	var after domain.ID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		sources, err := store.SearchSources(ctx, after, searchRebuildBatchSize)
		if err != nil {
			return err
		}
		for _, source := range sources {
			expected, err := sourceSearchText(source)
			if err != nil {
				return storedEntryError(source.ID)
			}
			result.Total++
			if source.ContentText == expected && source.FTSCopies == 1 && source.FTSStoredTextCopies == 1 && result.StoredVersion == domain.ContentNormalizerVersion {
				result.Ready++
			} else {
				result.Drifted++
			}
			after = source.ID
		}
		if len(sources) < searchRebuildBatchSize {
			return nil
		}
	}
}

func (service *SearchService) writeAllDerived(ctx context.Context, store SearchStore) error {
	var after domain.ID
	for {
		sources, err := store.SearchSources(ctx, after, searchRebuildBatchSize)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if err := ctx.Err(); err != nil {
				return err
			}
			contentText, err := sourceSearchText(source)
			if err != nil {
				return storedEntryError(source.ID)
			}
			if err := store.UpdateSearchDerived(ctx, source.ID, contentText); err != nil {
				return err
			}
			after = source.ID
		}
		if len(sources) < searchRebuildBatchSize {
			return nil
		}
	}
}

func prepareSearchQuery(raw string) (SearchRepositoryQuery, string, error) {
	if !utf8.ValidString(raw) || strings.TrimSpace(raw) == "" {
		return SearchRepositoryQuery{}, "", domain.NewError(domain.CodeInvalidArgument, "q must contain searchable text", map[string]any{"field": "q"})
	}
	if count := utf8.RuneCountInString(raw); count > MaxSearchQueryCodePoints {
		return SearchRepositoryQuery{}, "", domain.NewError(domain.CodeInvalidArgument, "q exceeds the code-point limit", map[string]any{"field": "q", "limit": MaxSearchQueryCodePoints})
	}
	normalized, err := domain.NormalizeContentV2(raw)
	if err != nil || normalized == "" {
		return SearchRepositoryQuery{}, "", domain.NewError(domain.CodeInvalidArgument, "q must contain searchable text", map[string]any{"field": "q"})
	}
	if utf8.RuneCountInString(normalized) <= 2 {
		return SearchRepositoryQuery{Mode: SearchModeLike, QueryValue: "%" + escapeLike(normalized) + "%"}, normalized, nil
	}
	if !strings.ContainsFunc(normalized, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
		return SearchRepositoryQuery{}, "", domain.NewError(domain.CodeInvalidArgument, "q must contain a letter or number", map[string]any{"field": "q"})
	}
	return SearchRepositoryQuery{Mode: SearchModeFTS, QueryValue: `"` + strings.ReplaceAll(normalized, `"`, `""`) + `"`}, normalized, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func searchDateContext(fromDate, toDate, timezone string) (*time.Location, *int64, *int64, error) {
	if err := validateTimezone(timezone); err != nil {
		return nil, nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is invalid", map[string]any{"field": "timezone"})
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is unavailable", map[string]any{"field": "timezone"})
	}
	fromMS, toMS, err := postDateRange(fromDate, toDate, timezone)
	return location, fromMS, toMS, err
}

func aggregateYears(instants []int64, location *time.Location) []YearCount {
	counts := make(map[int]int)
	for _, instant := range instants {
		counts[domain.TimeFromMillis(instant).In(location).Year()]++
	}
	years := make([]int, 0, len(counts))
	for year := range counts {
		years = append(years, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	result := make([]YearCount, 0, len(years))
	for _, year := range years {
		result = append(result, YearCount{Year: year, Count: counts[year]})
	}
	return result
}

func aggregateMonths(instants []int64, location *time.Location) []ArchiveMonthCount {
	type key struct{ year, month int }
	counts := make(map[key]int)
	for _, instant := range instants {
		local := domain.TimeFromMillis(instant).In(location)
		counts[key{local.Year(), int(local.Month())}]++
	}
	keys := make([]key, 0, len(counts))
	for value := range counts {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].year > keys[j].year || (keys[i].year == keys[j].year && keys[i].month > keys[j].month)
	})
	result := make([]ArchiveMonthCount, 0, len(keys))
	for _, value := range keys {
		result = append(result, ArchiveMonthCount{Year: value.year, Month: value.month, Count: counts[value]})
	}
	return result
}

func aggregateDays(instants []int64, location *time.Location) []ArchiveDayCount {
	counts := make(map[int]int)
	for _, instant := range instants {
		counts[domain.TimeFromMillis(instant).In(location).Day()]++
	}
	days := make([]int, 0, len(counts))
	for day := range counts {
		days = append(days, day)
	}
	sort.Ints(days)
	result := make([]ArchiveDayCount, 0, len(days))
	for _, day := range days {
		result = append(result, ArchiveDayCount{Day: day, Count: counts[day]})
	}
	return result
}

func searchSnippet(content, query string) string {
	const maxRunes = 160
	contentRunes := []rune(content)
	if len(contentRunes) <= maxRunes {
		return content
	}
	index := strings.Index(content, query)
	start := 0
	if index >= 0 {
		start = utf8.RuneCountInString(content[:index]) - 40
		if start < 0 {
			start = 0
		}
	}
	end := min(start+maxRunes, len(contentRunes))
	if end-start < maxRunes && end == len(contentRunes) {
		start = max(0, end-maxRunes)
	}
	snippet := string(contentRunes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(contentRunes) {
		snippet += "…"
	}
	return snippet
}

func invalidArchiveMonth() error {
	return domain.NewError(domain.CodeInvalidArgument, "month must use YYYY-MM", map[string]any{"field": "month"})
}

func normalizeSearchRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return domain.NewError(domain.CodeInternal, "search storage operation failed", nil)
}

func searchDataIntegrity(message string) error {
	return domain.NewError(domain.CodeDataIntegrityFailed, fmt.Sprintf("search index %s", message), nil)
}
