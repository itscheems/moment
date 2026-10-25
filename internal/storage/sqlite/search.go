package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// SearchRepository adapts snapshot queries and derived-only rebuild writes.
type SearchRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
}

var _ app.SearchTransactions = (*SearchRepository)(nil)

// NewSearchRepository constructs the ordinary snapshot/rebuild adapter.
func NewSearchRepository(transactions Transactor, locks *runtimefs.Locker) (*SearchRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &SearchRepository{transactions: transactions, locks: locks}, nil
}

// NewSearchRepositoryWithinMaintenance reuses the caller-held shared
// maintenance lease selected before CURRENT was resolved.
func NewSearchRepositoryWithinMaintenance(transactions Transactor, maintenance *runtimefs.Lease, locks *runtimefs.Locker) (*SearchRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if !maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		return nil, errors.New("shared maintenance lease is required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &SearchRepository{transactions: transactions, locks: locks, maintenance: maintenance}, nil
}

func (repository *SearchRepository) Read(ctx context.Context, operation func(app.SearchStore) error) error {
	if operation == nil {
		return errors.New("search read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error {
		return operation(searchStore{queries: queries})
	})
}

func (repository *SearchRepository) Write(ctx context.Context, operation func(app.SearchStore) error) error {
	if operation == nil {
		return errors.New("search write operation is required")
	}
	if repository.maintenance != nil {
		if !repository.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
			return errors.New("search maintenance lease is no longer held")
		}
		return repository.write(ctx, operation)
	}
	lease, err := repository.locks.Acquire(ctx, runtimefs.MaintenanceLock, runtimefs.Shared)
	if err != nil {
		return fmt.Errorf("coordinate search rebuild: %w", err)
	}
	defer func() { _ = lease.Release() }()
	return repository.write(ctx, operation)
}

func (repository *SearchRepository) write(ctx context.Context, operation func(app.SearchStore) error) error {
	return repository.transactions.WriteTx(ctx, func(queries Queries) error {
		return operation(searchStore{queries: queries})
	})
}

type searchStore struct{ queries Queries }

var _ app.SearchStore = searchStore{}

func (store searchStore) EntryMediaUsages(ctx context.Context, entryID domain.ID) ([]app.MediaUsageProjection, error) {
	return newPostStore(store.queries).EntryMediaUsages(ctx, entryID)
}

func (store searchStore) ArticleCards(ctx context.Context, ids []domain.ID) (map[domain.ID]app.ArticleCard, error) {
	return newPostStore(store.queries).ArticleCards(ctx, ids)
}

func (store searchStore) EntryLinkCards(ctx context.Context, entryID domain.ID) ([]app.LinkCardProjection, error) {
	return newPostStore(store.queries).EntryLinkCards(ctx, entryID)
}

func (store searchStore) EntryLocation(ctx context.Context, entryID domain.ID) (*domain.Location, error) {
	return newPostStore(store.queries).EntryLocation(ctx, entryID)
}

func (store searchStore) Timezone(ctx context.Context) (string, error) {
	var timezone string
	if err := store.queries.QueryRowContext(ctx, `SELECT timezone FROM site_settings WHERE id = 1`).Scan(&timezone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.NewError(domain.CodeDataIntegrityFailed, "site settings singleton is missing", nil)
		}
		return "", fmt.Errorf("read search timezone: %w", err)
	}
	return timezone, nil
}

func (store searchStore) SearchEntries(ctx context.Context, query app.SearchRepositoryQuery, limit int) ([]app.SearchRecord, error) {
	statement, arguments, err := searchSelect(query, `e.author_user_id, e.id, e.kind, e.content_md, e.visibility, e.created_at_ms, e.updated_at_ms, e.published_at_ms, e.reposted_at_ms, e.edited_at_ms, e.content_text`)
	if err != nil {
		return nil, err
	}
	if query.Position != nil {
		statement += `
  AND (rank > ? OR (rank = ? AND (published_at_ms < ? OR (published_at_ms = ? AND id < ?))))`
		arguments = append(arguments,
			query.Position.Rank, query.Position.Rank,
			query.Position.PublishedAtMS, query.Position.PublishedAtMS, query.Position.ID.String(),
		)
	}
	statement += `
ORDER BY rank ASC, published_at_ms DESC, id DESC
LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := store.queries.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query search entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]app.SearchRecord, 0, limit)
	for rows.Next() {
		record, err := scanSearchRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan search entry: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search entries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range records {
		records[i].Entry.MapCard, err = newPostStore(store.queries).entryMapCard(ctx, records[i].Entry.ID)
		if err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (store searchStore) CommentCounts(ctx context.Context, ids []domain.ID) (map[domain.ID]int, error) {
	return queryCommentCounts(ctx, store.queries, ids)
}

func (store searchStore) SearchPublishedMillis(ctx context.Context, query app.SearchRepositoryQuery) ([]int64, error) {
	statement, arguments, err := searchSelect(query, `published_at_ms`)
	if err != nil {
		return nil, err
	}
	statement = `SELECT published_at_ms FROM (` + statement + `)`
	return queryMillis(ctx, store.queries, statement, arguments...)
}

func (store searchStore) ArchivePublishedMillis(ctx context.Context, viewer app.Viewer, fromMS, toMS *int64) ([]int64, error) {
	visible, visibleArguments := visibilityPredicate(viewer, "visibility")
	// The archive calendar counts every post, articles included.
	statement := `SELECT published_at_ms FROM entries WHERE ` + visible
	arguments := append([]any{}, visibleArguments...)
	statement, arguments = appendDateRange(statement, arguments, "published_at_ms", fromMS, toMS)
	return queryMillis(ctx, store.queries, statement, arguments...)
}

func searchSelect(query app.SearchRepositoryQuery, columns string) (string, []any, error) {
	var statement string
	var arguments []any
	switch query.Mode {
	case app.SearchModeFTS:
		statement = `SELECT ` + columns + `, bm25(entry_fts) AS rank
FROM entry_fts
JOIN entries AS e ON e.id = entry_fts.entry_id
WHERE entry_fts MATCH ?`
		arguments = []any{query.QueryValue}
	case app.SearchModeLike:
		statement = `SELECT ` + columns + `, 0.0 AS rank
FROM entries AS e
WHERE e.content_text LIKE ? ESCAPE '\'`
		arguments = []any{query.QueryValue}
	default:
		return "", nil, errors.New("unsupported prepared search mode")
	}
	visible, visibleArguments := visibilityPredicate(query.Viewer, "e.visibility")
	statement += "\n  AND " + visible
	arguments = append(arguments, visibleArguments...)
	statement, arguments = appendDateRange(statement, arguments, "e.published_at_ms", query.FromMS, query.ToMS)
	return statement, arguments, nil
}

func appendDateRange(statement string, arguments []any, column string, fromMS, toMS *int64) (string, []any) {
	if fromMS != nil {
		statement += "\n  AND " + column + " >= ?"
		arguments = append(arguments, *fromMS)
	}
	if toMS != nil {
		statement += "\n  AND " + column + " < ?"
		arguments = append(arguments, *toMS)
	}
	return statement, arguments
}

func queryMillis(ctx context.Context, queries Queries, statement string, arguments ...any) ([]int64, error) {
	rows, err := queries.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query archive instants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var values []int64
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan archive instant: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate archive instants: %w", err)
	}
	return values, nil
}

func (store searchStore) SearchNormalizerVersion(ctx context.Context) (int, error) {
	var version int
	if err := store.queries.QueryRowContext(ctx, `SELECT normalizer_version FROM search_index_state WHERE id = 1`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read search normalizer version: %w", err)
	}
	return version, nil
}

func (store searchStore) SearchRevision(ctx context.Context) (int64, error) {
	var revision int64
	if err := store.queries.QueryRowContext(ctx, `SELECT revision FROM search_index_state WHERE id = 1`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read search revision: %w", err)
	}
	return revision, nil
}

func (store searchStore) SearchOrphanCount(ctx context.Context) (int, error) {
	var count int
	if err := store.queries.QueryRowContext(ctx, `
SELECT count(*)
FROM entry_fts AS f
LEFT JOIN entries AS e ON e.id = f.entry_id
WHERE e.id IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count orphan search rows: %w", err)
	}
	return count, nil
}

// searchSourceCard is the slice of a stored link card that reaches search:
// exactly the fields domain.LinkCardSearchText reads, carried as JSON so one
// row can hold every card of an entry in position order.
type searchSourceCard struct {
	Title        string `json:"title"`
	SiteName     string `json:"site_name"`
	AuthorName   string `json:"author_name"`
	CanonicalURL string `json:"canonical_url"`
}

// searchSourceCardsSQL is the subquery that carries an entry's cards as one
// JSON column, for the correlated entry aliased e. Every reader that
// recomposes indexed text -- the drift check, the rebuild, the version
// bootstrap -- selects it, so they all hand the combiner the same fields.
const searchSourceCardsSQL = `coalesce((
           SELECT json_group_array(json_object(
               'title', card.title, 'site_name', card.site_name,
               'author_name', card.author_name, 'canonical_url', card.canonical_url))
           FROM (
               SELECT title, site_name, author_name, canonical_url
               FROM entry_link_card
               WHERE entry_id = e.id
               ORDER BY position
           ) AS card
       ), '[]')`

func decodeSearchSourceCards(cardsJSON string) ([]domain.LinkCard, error) {
	var storedCards []searchSourceCard
	if err := json.Unmarshal([]byte(cardsJSON), &storedCards); err != nil {
		return nil, fmt.Errorf("decode search source cards: %w", err)
	}
	cards := make([]domain.LinkCard, len(storedCards))
	for index, card := range storedCards {
		cards[index] = domain.LinkCard{Title: card.Title, SiteName: card.SiteName, AuthorName: card.AuthorName, CanonicalURL: card.CanonicalURL}
	}
	return cards, nil
}

func (store searchStore) SearchSources(ctx context.Context, after domain.ID, limit int) ([]app.SearchSource, error) {
	// The cards come back as columns, not as text: the text an entry is
	// indexed under is composed by domain.LinkCardSearchText on the write
	// path, and composing it here from the same fields is what keeps a
	// rebuild and the drift check agreeing with what Create wrote. A SQL
	// concatenation once stood in for that call and disagreed with it -- it
	// carried the whole canonical URL, which the normalizer drops, where the
	// combiner keeps the host -- so every entry with a card read as drifted.
	rows, err := store.queries.QueryContext(ctx, `
SELECT e.id, e.kind, e.content_md, e.content_text,
       coalesce(a.title, ''), coalesce(a.body_json, ''),
       `+searchSourceCardsSQL+`,
       count(f.rowid),
       coalesce(sum(CASE WHEN f.content_text = e.content_text THEN 1 ELSE 0 END), 0)
FROM entries AS e
LEFT JOIN entry_fts AS f ON f.entry_id = e.id
LEFT JOIN entry_article AS a ON a.entry_id = e.id
WHERE (? = '' OR e.id > ?)
GROUP BY e.id, e.kind, e.content_md, e.content_text, a.title, a.body_json
ORDER BY e.id
LIMIT ?`, after.String(), after.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("query search sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	sources := make([]app.SearchSource, 0, limit)
	for rows.Next() {
		var idString, kind, cardsJSON string
		var contentMD sql.NullString
		var source app.SearchSource
		if err := rows.Scan(&idString, &kind, &contentMD, &source.ContentText,
			&source.ArticleTitle, &source.ArticleBodyJSON,
			&cardsJSON, &source.FTSCopies, &source.FTSStoredTextCopies); err != nil {
			return nil, fmt.Errorf("scan search source: %w", err)
		}
		source.Kind = domain.EntryKind(kind)
		source.ContentMD = nullableText(contentMD)
		cards, err := decodeSearchSourceCards(cardsJSON)
		if err != nil {
			return nil, err
		}
		source.LinkCardText = domain.LinkCardSearchText(cards)
		id, err := domain.ParseID(idString)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored search source has an invalid ID", nil)
		}
		source.ID = id
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range sources {
		sources[i].MapCard, err = newPostStore(store.queries).entryMapCard(ctx, sources[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return sources, nil
}

func (store searchStore) ClearSearchIndex(ctx context.Context) error {
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_fts`); err != nil {
		return fmt.Errorf("clear search index: %w", err)
	}
	return nil
}

func (store searchStore) UpdateSearchDerived(ctx context.Context, id domain.ID, contentText string) error {
	result, err := store.queries.ExecContext(ctx, `
UPDATE entries
SET content_text = ?
WHERE id = ?`, contentText, id.String())
	if err != nil {
		return fmt.Errorf("update entry search text: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("update entry search text changed %d rows: %w", changed, err)
	}
	return nil
}

func (store searchStore) SetSearchNormalizerVersion(ctx context.Context, version int) error {
	result, err := store.queries.ExecContext(ctx, `UPDATE search_index_state SET normalizer_version = ? WHERE id = 1`, version)
	if err != nil {
		return fmt.Errorf("update search normalizer version: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("update search normalizer version changed %d rows: %w", changed, err)
	}
	return nil
}

func (store searchStore) AdvanceSearchRevision(ctx context.Context) (int64, error) {
	result, err := store.queries.ExecContext(ctx, `UPDATE search_index_state SET revision = revision + 1 WHERE id = 1`)
	if err != nil {
		return 0, fmt.Errorf("advance search revision: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return 0, fmt.Errorf("advance search revision changed %d rows: %w", changed, err)
	}
	return store.SearchRevision(ctx)
}

func scanSearchRecord(scanner entryScanner) (app.SearchRecord, error) {
	var (
		author                            sql.NullString
		idString                          string
		kind                              string
		contentMD                         sql.NullString
		contentText                       string
		visibility                        string
		createdAt, updatedAt, publishedAt int64
		repostedAt                        sql.NullInt64
		editedAt                          sql.NullInt64
		rank                              float64
	)
	if err := scanner.Scan(&author, &idString, &kind, &contentMD, &visibility, &createdAt, &updatedAt, &publishedAt, &repostedAt, &editedAt, &contentText, &rank); err != nil {
		return app.SearchRecord{}, err
	}
	id, err := domain.ParseID(idString)
	if err != nil {
		return app.SearchRecord{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored search entry has an invalid ID", nil)
	}
	entry := domain.Entry{
		AuthorUserID: author.String,
		ID:           id, Kind: domain.EntryKind(kind), ContentMD: nullableText(contentMD), Visibility: domain.Visibility(visibility),
		CreatedAt: domain.TimeFromMillis(createdAt), UpdatedAt: domain.TimeFromMillis(updatedAt), PublishedAt: domain.TimeFromMillis(publishedAt),
	}
	if editedAt.Valid {
		value := domain.TimeFromMillis(editedAt.Int64)
		entry.EditedAt = &value
	}
	if repostedAt.Valid {
		value := domain.TimeFromMillis(repostedAt.Int64)
		entry.RepostedAt = &value
	}
	if err := entry.Validate(); err != nil {
		return app.SearchRecord{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored search entry violates the domain contract", map[string]any{"post_id": id.String()})
	}
	return app.SearchRecord{Entry: entry, ContentText: contentText, Rank: rank}, nil
}

func (store searchStore) EntryByID(ctx context.Context, id domain.ID) (domain.Entry, bool, error) {
	return newPostStore(store.queries).EntryByID(ctx, id)
}
func (store searchStore) EntryQuote(ctx context.Context, id domain.ID) (app.EntryQuoteReference, error) {
	return newPostStore(store.queries).EntryQuote(ctx, id)
}
func (store searchStore) CommentByID(ctx context.Context, id domain.ID) (domain.Comment, bool, error) {
	return newCommentStore(store.queries).CommentByID(ctx, id)
}
func (store searchStore) CommentMediaUsages(ctx context.Context, id domain.ID) ([]app.MediaUsageProjection, error) {
	return newCommentStore(store.queries).CommentMediaUsages(ctx, id)
}
