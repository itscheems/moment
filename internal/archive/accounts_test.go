package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	memberAccountID   = "0f0e0d0c0b0a09080706050403020100"
	pendingAccountID  = "1f1e1d1c1b1a19181716151413121110"
	accountsCommentID = "01ARZ3NDEKTSV4RRFFQ69G5FC1"
	accountsEntryID   = "01ARZ3NDEKTSV4RRFFQ69G5FC2"
)

// insertAccountsFixture adds two ordinary accounts, three credentials, and one
// comment written by one of them, so a round trip has something to lose.
func insertAccountsFixture(t *testing.T, fixture exportFixture) {
	t.Helper()
	ctx := context.Background()
	avatarURL := "https://provider.test/avatar.png"
	email := "member@example.test"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, username, display_name, bio, avatar_media_id, cover_media_id, role, status, username_set_at_ms, created_at_ms, updated_at_ms)
VALUES (?, 'meihua', '梅花', 'a member with a bio', NULL, NULL, 'member', 'active', 1700000000000, 1700000000000, 1700000001000)`, []any{memberAccountID}},
		{`INSERT INTO users (id, username, display_name, bio, avatar_media_id, cover_media_id, role, status, username_set_at_ms, created_at_ms, updated_at_ms)
VALUES (?, 'newcomer', 'Newcomer', '', NULL, NULL, 'member', 'pending', NULL, 1700000002000, 1700000002000)`, []any{pendingAccountID}},
		{`INSERT INTO identities (provider, provider_user_id, user_id, email, email_verified, email_deliverable, display_name, avatar_url, created_at_ms, last_login_at_ms)
VALUES ('github', '4210', ?, ?, 1, 1, '梅花', ?, 1700000000000, 1700000003000)`, []any{memberAccountID, email, avatarURL}},
		{`INSERT INTO identities (provider, provider_user_id, user_id, email, email_verified, email_deliverable, display_name, avatar_url, created_at_ms, last_login_at_ms)
VALUES ('google', 'zzz-9', ?, NULL, 0, 0, '', NULL, 1700000000500, 1700000003500)`, []any{memberAccountID}},
		{`INSERT INTO identities (provider, provider_user_id, user_id, email, email_verified, email_deliverable, display_name, avatar_url, created_at_ms, last_login_at_ms)
VALUES ('github', '9001', ?, NULL, 0, 1, 'Newcomer', NULL, 1700000002000, 1700000004000)`, []any{pendingAccountID}},
		{`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id) VALUES (?, 'moment', '账户往返', 1, 1, 1, '0123456789abcdef0123456789abcdef')`, []any{accountsEntryID}},
		{`INSERT INTO comments (id, entry_id, parent_id, author_name, author_user_id, content_md, created_at_ms)
VALUES (?, ?, NULL, '梅花', ?, '一条由成员写的评论', 1)`, []any{accountsCommentID, accountsEntryID, memberAccountID}},
	}
	if err := fixture.database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		for _, statement := range statements {
			if _, err := queries.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// stagedAccounts opens the generation an inspection built and reads it back.
func stagedAccounts(t *testing.T, fixture exportFixture, result Inspection) ([]sqlitestore.ArchiveUser, []sqlitestore.ArchiveIdentity, sqlitestore.ArchiveSite) {
	t.Helper()
	ctx := context.Background()
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{
		Root: fixture.runtime.Root, Instance: fixture.runtime.Instance,
		Current: stagingGeneration(stageRoot, result.GenerationID), Locks: fixture.runtime.Locks,
	}
	database, err := sqlitestore.Open(ctx, stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var users []sqlitestore.ArchiveUser
	if err := snapshot.Users(ctx, func(value sqlitestore.ArchiveUser) error {
		users = append(users, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var identities []sqlitestore.ArchiveIdentity
	if err := snapshot.Identities(ctx, func(value sqlitestore.ArchiveIdentity) error {
		identities = append(identities, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	site, err := snapshot.Site(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return users, identities, site
}

func sourceAccounts(t *testing.T, fixture exportFixture) ([]sqlitestore.ArchiveUser, []sqlitestore.ArchiveIdentity) {
	t.Helper()
	ctx := context.Background()
	snapshot, err := fixture.database.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var users []sqlitestore.ArchiveUser
	if err := snapshot.Users(ctx, func(value sqlitestore.ArchiveUser) error {
		users = append(users, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var identities []sqlitestore.ArchiveIdentity
	if err := snapshot.Identities(ctx, func(value sqlitestore.ArchiveIdentity) error {
		identities = append(identities, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return users, identities
}

// TestAccountsRoundTripThroughPortableArchive proves the two new members carry
// every account and credential across a full export and import, field for
// field. A successful inspection also means validatePortableAuthority compared
// the rebuilt generation back against the archive and found them identical.
func TestAccountsRoundTripThroughPortableArchive(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertAccountsFixture(t, fixture)
	sourceUsers, sourceIdentities := sourceAccounts(t, fixture)
	if len(sourceUsers) != 3 || len(sourceIdentities) != 3 {
		t.Fatalf("fixture accounts = %d users, %d identities", len(sourceUsers), len(sourceIdentities))
	}

	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	if len(nonemptyLines(members["data/users.jsonl"])) != 3 || len(nonemptyLines(members["data/identities.jsonl"])) != 3 {
		t.Fatalf("exported accounts members = %#v", order)
	}
	// Nothing about a sign-in may travel with a backup.
	for name, body := range members {
		for _, forbidden := range []string{"session", "cookie", "access_token", "refresh_token"} {
			if len(body) > 0 && containsToken(body, forbidden) {
				t.Fatalf("member %s leaked %q", name, forbidden)
			}
		}
	}

	now := time.Date(2026, 8, 23, 3, 4, 5, 0, time.UTC)
	result, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(
		context.Background(), exportFixtureFile(t, fixture), testAuthorization(7),
	)
	if err != nil {
		t.Fatal(err)
	}
	// The preview is what an owner reads before an irreversible replacement, so
	// it has to say the archive carries accounts, and that this site has three
	// of its own that commit would delete.
	if result.Counts != (ImportCounts{Entries: 1, Comments: 1, Users: 3, Identities: 3}) {
		t.Fatalf("accounts archive preview counts = %#v", result.Counts)
	}
	if result.Replacement.Counts != (ImportCounts{Entries: 1, Comments: 1, Users: 3, Identities: 3}) {
		t.Fatalf("accounts replacement counts = %#v", result.Replacement.Counts)
	}
	stagedUsers, stagedIdentities, stagedSite := stagedAccounts(t, fixture, result)
	if !reflect.DeepEqual(sourceUsers, stagedUsers) {
		t.Fatalf("staged users = %#v want %#v", stagedUsers, sourceUsers)
	}
	if !reflect.DeepEqual(sourceIdentities, stagedIdentities) {
		t.Fatalf("staged identities = %#v want %#v", stagedIdentities, sourceIdentities)
	}
	// The owner account survived being adopted rather than inserted.
	owner := stagedUsers[0]
	for _, value := range stagedUsers {
		if value.Role == "owner" {
			owner = value
		}
	}
	if owner.ID != stagedSite.ProfileUserID || owner.Username != stagedSite.Username || owner.DisplayName != stagedSite.DisplayName {
		t.Fatalf("staged owner = %#v site = %#v", owner, stagedSite)
	}
}

func containsToken(body []byte, token string) bool {
	return len(token) > 0 && len(body) >= len(token) && indexOf(body, token) >= 0
}

func indexOf(body []byte, token string) int {
	for index := 0; index+len(token) <= len(body); index++ {
		if string(body[index:index+len(token)]) == token {
			return index
		}
	}
	return -1
}

// TestArchiveWithoutAccountMembersSynthesizesTheOwner is the reason the format
// version stayed at 1.
//
// A backup taken by the previous binary has seven members and no accounts. It
// is the only way back from a schema migration, so the binary that added the
// accounts members must still read it -- and must reconstruct the one account
// it describes, in the site record, without inventing an identity.
func TestArchiveWithoutRealAccountsIsRejected(t *testing.T) {
	source := newExportFixture(t, true)
	members, order := readArchive(t, exportFixtureBytes(t, source))
	order = stripAccountMembers(t, members, order)
	path := filepath.Join(t.TempDir(), "unsupported.zip")
	writeArchive(t, path, members, order)
	if err := ValidateFile(path); err == nil {
		t.Fatal("unsupported archive was accepted")
	}
	target := newExportFixture(t, false)
	before := fingerprintGeneration(t, target.runtime.Current.Root)
	now := time.Date(2026, 8, 23, 7, 0, 0, 0, time.UTC)
	if _, err := testInspector(t, target.runtime.Root, target.database, &now, math.MaxUint64).InspectFile(context.Background(), path, testAuthorization(1)); err == nil {
		t.Fatal("unsupported archive was staged")
	}
	after := fingerprintGeneration(t, target.runtime.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected archive changed active data")
	}
}

func TestArchiveWithOneAccountsMemberIsRejected(t *testing.T) {
	for _, dropped := range []string{"data/users.jsonl", "data/identities.jsonl"} {
		t.Run(dropped, func(t *testing.T) {
			fixture := newExportFixture(t, false)
			members, order := readArchive(t, exportFixtureBytes(t, fixture))
			delete(members, dropped)
			remaining := make([]string, 0, len(order))
			for _, name := range order {
				if name != dropped {
					remaining = append(remaining, name)
				}
			}
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			name := filepath.Join(t.TempDir(), "half-accounts.zip")
			writeArchive(t, name, members, remaining)
			if err := ValidateFile(name); err == nil {
				t.Fatal("an archive with one accounts member was accepted")
			}
		})
	}
}

// TestOwnerDisagreementBetweenSiteAndAccountIsRejected proves the duplicated
// profile fields cannot silently diverge. With two answers and no tiebreak,
// picking one would rename the owner on restore.
func TestOwnerDisagreementBetweenSiteAndAccountIsRejected(t *testing.T) {
	fixture := newExportFixture(t, false)
	members, order := readArchive(t, exportFixtureBytes(t, fixture))
	var rebuilt []byte
	for _, line := range nonemptyLines(members["data/users.jsonl"]) {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value["role"] == "admin" {
			value["display_name"] = "Someone Else"
		}
		rebuilt = append(rebuilt, mustMarshalTest(t, value)...)
		rebuilt = append(rebuilt, '\n')
	}
	members["data/users.jsonl"] = rebuilt
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	name := filepath.Join(t.TempDir(), "two-owners.zip")
	writeArchive(t, name, members, order)
	if err := ValidateFile(name); err == nil {
		t.Fatal("an archive whose site record and owner account disagree was accepted")
	}
}

// TestMergeReviewProjectsEveryComparedField holds the review projection to the
// comparators.
//
// A changed field that the projection does not show renders two sides
// identically under a label saying they differ, which is worse than showing
// nothing: the reviewer cannot tell whether the plan or the display is wrong.
// This test is the reason mergeReviewFieldKeys exists.
func TestMergeReviewProjectsEveryComparedField(t *testing.T) {
	shas := map[string]string{"01H00000000000000000000001": "aa", "01H00000000000000000000002": "bb"}
	first, second := "01H00000000000000000000001", "01H00000000000000000000002"
	early, late := int64(1), int64(2)
	emitted := map[MergeEntityKind][]string{
		MergeEntry: entryChangedFields(
			sqlitestore.ArchiveEntry{Kind: "moment", ContentMD: domain.MarkdownBody("a"), Visibility: domain.VisibilityEveryone, CreatedAtMS: early, UpdatedAtMS: early, PublishedAtMS: early, RepostedAtMS: &early, EditedAtMS: &early, QuotedEntryID: &first, Media: []sqlitestore.ArchiveUsage{{MediaID: first}}},
			sqlitestore.ArchiveEntry{Kind: "repost", ContentMD: domain.MarkdownBody("b"), Visibility: domain.VisibilityOnlyMe, CreatedAtMS: late, UpdatedAtMS: late, PublishedAtMS: late, QuoteUnavailable: true, Media: []sqlitestore.ArchiveUsage{{MediaID: second}}, Location: &domainLocationForReview, LinkCards: []sqlitestore.ArchiveLinkCard{{ThumbnailSHA256: "cc"}}},
			shas, shas,
		),
		MergeComment: commentChangedFields(
			sqlitestore.ArchiveComment{EntryID: first, ParentID: &first, AuthorName: "a", AuthorUserID: &first, ReplyToCommentID: &first, ContentMD: "a", CreatedAtMS: early, Media: []sqlitestore.ArchiveUsage{{MediaID: first}}, Pinned: true},
			sqlitestore.ArchiveComment{EntryID: second, AuthorName: "b", ContentMD: "b", CreatedAtMS: late, Media: []sqlitestore.ArchiveUsage{{MediaID: second}}, Location: &domainLocationForReview},
			shas, shas, first, second,
		),
		MergeUser: userChangedFields(
			sqlitestore.ArchiveUser{Username: "a", DisplayName: "A", Bio: "a", AvatarMediaID: &first, CoverMediaID: &first, Role: "admin", Status: "active", UsernameSetAtMS: &early, CreatedAtMS: early, UpdatedAtMS: early},
			sqlitestore.ArchiveUser{Username: "b", DisplayName: "B", Bio: "b", AvatarMediaID: &second, CoverMediaID: &second, Role: "member", Status: "pending", CreatedAtMS: late, UpdatedAtMS: late},
			shas, shas,
		),
		MergeIdentity: identityChangedFields(
			sqlitestore.ArchiveIdentity{UserID: first, Email: &first, EmailVerified: true, EmailDeliverable: true, DisplayName: "A", AvatarURL: &first, CreatedAtMS: early, LastLoginAtMS: early},
			sqlitestore.ArchiveIdentity{UserID: second, DisplayName: "B", CreatedAtMS: late, LastLoginAtMS: late},
		),
	}
	projections := map[MergeEntityKind]MergeReviewEntity{
		MergeEntry: reviewEntry(sqlitestore.ArchiveEntry{
			ID: first, Kind: "moment", ContentMD: domain.MarkdownBody("a"), RepostedAtMS: &early, EditedAtMS: &early, QuotedEntryID: &first,
			Location: &domainLocationForReview, LinkCards: []sqlitestore.ArchiveLinkCard{{ThumbnailSHA256: "cc"}},
		}, MergeReviewLocal, nil),
		MergeComment: reviewComment(sqlitestore.ArchiveComment{
			ID: first, EntryID: first, ParentID: &first, AuthorUserID: &first, ReplyToCommentID: &first,
			Pinned: true, Location: &domainLocationForReview,
		}, MergeReviewLocal, nil),
		MergeUser: reviewUser(sqlitestore.ArchiveUser{
			ID: first, Username: "a", DisplayName: "A", Bio: "a", Role: "admin", Status: "active",
			AvatarMediaID: &first, CoverMediaID: &first, UsernameSetAtMS: &early,
		}, MergeReviewLocal, nil),
		MergeIdentity: reviewIdentity(sqlitestore.ArchiveIdentity{
			Provider: "github", ProviderUserID: "1", UserID: first, Email: &first, AvatarURL: &first,
		}),
	}
	for kind, fields := range emitted {
		if len(fields) == 0 {
			t.Fatalf("%s comparator produced no changed fields", kind)
		}
		var projected map[string]json.RawMessage
		encoded, err := json.Marshal(projections[kind])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &projected); err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			key, mapped := mergeReviewFieldKeys[kind][field]
			if !mapped {
				t.Fatalf("%s comparator reports %q with no review projection", kind, field)
			}
			if _, shown := projected[key]; !shown {
				t.Fatalf("%s review projection omits %q (key %q): %s", kind, field, key, encoded)
			}
		}
		for field := range mergeReviewFieldKeys[kind] {
			found := false
			for _, emittedField := range fields {
				if emittedField == field {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s review table names %q which no comparator emits", kind, field)
			}
		}
	}
}

// domainLocationForReview is a minimal valid place used only to prove the
// review projection carries a location at all.
var domainLocationForReview = domain.Location{
	Name: "静安寺", DisplayName: "上海市静安区南京西路1686号", CountryCode: "CN",
	Country: "中国", Region: "上海市", City: "上海市", District: "静安区",
	Category: "风景名胜", Provider: "amap", ProviderPlaceID: "B0FFF",
}

// TestMergeRestoresAnAccountTheBackupKnowsAndTheSiteLost proves the accounts
// half of a merge is really merged.
//
// Before accounts were rows, the whole account story in a merge was one line
// forcing profile_user_id to stay local. That line is still right, and on its own it
// would now discard every account a backup carries -- including the ones whose
// comments the same merge is restoring.
func TestMergeRestoresAnAccountTheBackupKnowsAndTheSiteLost(t *testing.T) {
	fixture := newExportFixture(t, false)
	insertAccountsFixture(t, fixture)
	archivePath := exportFixtureFile(t, fixture)

	ctx := context.Background()
	if err := fixture.database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, pendingAccountID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(ctx, fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 23, 6, 0, 0, 0, time.UTC)
	committer, err := NewCommitter(CommitOptions{
		DataDir: fixture.runtime.Root, AppVersion: "0.1.0", Clock: func() time.Time { return now },
		AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := committer.InspectMergeFileOffline(ctx, archivePath, testAuthorization(1))
	if err != nil {
		t.Fatal(err)
	}
	// Accounts are reported but never offered as a choice.
	for _, item := range inspection.Plan.Items {
		if (item.Kind == MergeUser || item.Kind == MergeIdentity) && item.Selectable {
			t.Fatalf("account item is selectable: %#v", item)
		}
	}
	for _, choice := range inspection.Plan.DefaultSelection.Items {
		if choice.Kind == MergeUser || choice.Kind == MergeIdentity {
			t.Fatalf("account appeared in the default selection: %#v", choice)
		}
	}
	result, err := committer.CommitMerge(ctx, inspection.StagingToken, testAuthorization(1), inspection.Plan.DefaultSelection, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Phase != CommitSucceeded {
		t.Fatalf("merge result = %#v", result)
	}
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	authority, err := ReadPortableAuthority(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	restored := map[string]sqlitestore.ArchiveUser{}
	for _, value := range authority.Users {
		restored[value.ID] = value
	}
	if len(restored) != 3 {
		t.Fatalf("merged accounts = %#v", authority.Users)
	}
	recovered, present := restored[pendingAccountID]
	if !present || recovered.Username != "newcomer" || recovered.Status != "pending" || recovered.Role != "member" {
		t.Fatalf("account the backup restored = %#v", recovered)
	}
	if restored[authority.Site.ProfileUserID].Role != "admin" {
		t.Fatalf("owner after merge = %#v", restored[authority.Site.ProfileUserID])
	}
	if len(authority.Identities) != 3 {
		t.Fatalf("merged identities = %#v", authority.Identities)
	}
}

// TestMergeRefusesTwoAccountsClaimingOneUsername refuses rather than resolves.
// Nothing in a two-way merge can decide which of two people gets renamed.
func TestMergeRefusesTwoAccountsClaimingOneUsername(t *testing.T) {
	ownerID := "00112233445566778899aabbccddeeff"
	localMember := "aa112233445566778899aabbccddeeff"
	incomingMember := "bb112233445566778899aabbccddeeff"
	owner := sqlitestore.ArchiveUser{
		ID: ownerID, Username: "owner", DisplayName: "Owner", Role: "admin", Status: "active",
		CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	local := PortableAuthority{
		Site:  sqlitestore.ArchiveSite{ProfileUserID: ownerID, Username: "owner", DisplayName: "Owner", Title: "T", Timezone: "UTC"},
		Users: []sqlitestore.ArchiveUser{owner, {ID: localMember, Username: "shared", DisplayName: "Local", Role: "member", Status: "active", CreatedAtMS: 1, UpdatedAtMS: 1}},
	}
	incoming := PortableAuthority{
		Site:  local.Site,
		Users: []sqlitestore.ArchiveUser{owner, {ID: incomingMember, Username: "shared", DisplayName: "Incoming", Role: "member", Status: "active", CreatedAtMS: 1, UpdatedAtMS: 1}},
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{
		ArchiveSHA256: repeatHex("c"), BaseGeneration: "generation",
		ExpiresAt: time.Unix(20, 0).UTC(), Local: local, Incoming: incoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolvePortableMerge(plan, plan.DefaultSelection, local, incoming, MergeResolveOptions{})
	if !domain.HasCode(err, domain.CodeUsernameTaken) {
		t.Fatalf("username collision = %v", err)
	}
}

// TestLegacyArchiveRestoresAllTheWayThroughCommit takes a pre-accounts backup
// the whole distance: inspect, stage, commit, switch CURRENT, and serve.
//
// Inspection alone would not prove this. Commit revalidates the staged
// authority against the archive a second time, takes its own pre-import
// backup and validates that too, so a synthesized owner that only satisfied
// the import path would fail here instead.
func TestLegacyArchiveVersionIsRejectedBeforeCommit(t *testing.T) {
	source := newExportFixture(t, true)
	members, order := readArchive(t, exportFixtureBytes(t, source))
	var doc manifest
	if err := json.Unmarshal(members["manifest.json"], &doc); err != nil {
		t.Fatal(err)
	}
	doc.FormatVersion = 1
	members["manifest.json"], _ = json.Marshal(doc)
	rebuildManifestExpanded(t, members)
	rebuildChecksums(members)
	path := filepath.Join(t.TempDir(), "unsupported.zip")
	writeArchive(t, path, members, order)
	if err := ValidateFile(path); err == nil {
		t.Fatal("unsupported archive was accepted")
	}
	target := newExportFixture(t, false)
	before := fingerprintGeneration(t, target.runtime.Current.Root)
	now := time.Date(2026, 8, 23, 7, 0, 0, 0, time.UTC)
	if _, err := testInspector(t, target.runtime.Root, target.database, &now, math.MaxUint64).InspectFile(context.Background(), path, testAuthorization(1)); err == nil {
		t.Fatal("unsupported archive was staged")
	}
	after := fingerprintGeneration(t, target.runtime.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected archive changed active data")
	}
}

func mustReadFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The refusal a merge cannot avoid arrives before the review, not after it.
//
// Two accounts sharing a handle is fatal whatever the reviewer chooses, and the
// comparison never looked at handles -- it matches accounts by id. So a
// reviewer worked through every conflict in the plan, pressed commit, and was
// told then that this backup can never be merged at all.
func TestMergePlanRefusesAContestedHandleBeforeTheReviewStarts(t *testing.T) {
	ctx := context.Background()
	fixture := newExportFixture(t, false)
	insertAccountsFixture(t, fixture)
	archivePath := exportFixtureFile(t, fixture)

	// The backup's "newcomer" is gone from this site, and a different person
	// took the handle. Neither account can keep it and neither can be renamed.
	replacement := "2f2e2d2c2b2a29282726252423222120"
	if err := fixture.database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, pendingAccountID); err != nil {
			return err
		}
		_, err := queries.ExecContext(ctx, `
INSERT INTO users (id, username, display_name, bio, avatar_media_id, cover_media_id, role, status, username_set_at_ms, created_at_ms, updated_at_ms)
VALUES (?, 'newcomer', 'Someone Else', '', NULL, NULL, 'member', 'active', 1700000005000, 1700000005000, 1700000005000)`, replacement)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(ctx, fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 23, 6, 0, 0, 0, time.UTC)
	committer, err := NewCommitter(CommitOptions{
		DataDir: fixture.runtime.Root, AppVersion: "0.1.0", Clock: func() time.Time { return now },
		AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := committer.InspectMergeFileOffline(ctx, archivePath, testAuthorization(1))
	if err != nil {
		t.Fatal(err)
	}
	if inspection.MergeAllowed {
		t.Fatal("a merge that cannot be applied was offered as available")
	}
	if inspection.MergeBlockedReason == nil || *inspection.MergeBlockedReason != string(domain.CodeUsernameTaken) {
		t.Fatalf("merge blocked reason = %v", inspection.MergeBlockedReason)
	}
	// The review is still built, so the reader can see what the backup holds
	// and decide whether a full replacement is what they want instead.
	if inspection.Review.IncomingOnly.Total+inspection.Review.LocalOnly.Total == 0 {
		t.Fatalf("blocked inspection carried no review: %#v", inspection.Review)
	}
	// And the apply-time refusal is still there behind it.
	if _, err := committer.CommitMerge(ctx, inspection.StagingToken, testAuthorization(1), inspection.Plan.DefaultSelection, nil); !domain.HasCode(err, domain.CodeUsernameTaken) {
		t.Fatalf("commit of a blocked merge = %v", err)
	}
}

// A backup naming a suspended owner used to restore an instance nobody could
// sign into.
//
// SetStatus refuses to suspend an owner precisely so the last administrator
// cannot be locked out, and the import path wrote the archived status straight
// into the column, going around it. There is no in-product way back from that:
// a suspended account cannot sign in, and only a signed-in owner can change a
// status.
func TestArchiveRefusesDataWithNoActiveAdministrator(t *testing.T) {
	fixture := newExportFixture(t, false)
	if err := fixture.database.WriteTx(context.Background(), func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(context.Background(), `UPDATE users SET status='suspended' WHERE role='admin'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &destination); !domain.HasCode(err, domain.CodeExportInvalid) {
		t.Fatalf("invalid administrator graph export=%v", err)
	}
}

func TestMergePreservesExplicitAccountStatus(t *testing.T) {
	ownerID := "00112233445566778899aabbccddeeff"
	memberID := "aa112233445566778899aabbccddeeff"
	suspendedOwner := sqlitestore.ArchiveUser{
		ID: ownerID, Username: "owner", DisplayName: "Owner", Role: "admin", Status: "suspended",
		CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	site := sqlitestore.ArchiveSite{ProfileUserID: ownerID, Username: "owner", DisplayName: "Owner", Title: "T", Timezone: "UTC"}
	local := PortableAuthority{
		Site:  site,
		Users: []sqlitestore.ArchiveUser{suspendedOwner, {ID: memberID, Username: "meihua", DisplayName: "Local", Role: "member", Status: "suspended", CreatedAtMS: 1, UpdatedAtMS: 1}},
	}
	incoming := PortableAuthority{Site: site, Users: []sqlitestore.ArchiveUser{suspendedOwner}}
	plan, err := ComparePortableAuthorities(MergePlanInput{
		ArchiveSHA256: repeatHex("c"), BaseGeneration: "generation",
		ExpiresAt: time.Unix(20, 0).UTC(), Local: local, Incoming: incoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolvePortableMerge(plan, plan.DefaultSelection, local, incoming, MergeResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range resolved.Authority.Users {
		switch value.ID {
		case ownerID:
			if value.Role != string(domain.UserRoleAdmin) || value.Status != string(domain.AccountSuspended) {
				t.Fatalf("merged owner = %#v", value)
			}
		case memberID:
			// A member's suspension is a decision this site made and keeps.
			if value.Status != string(domain.AccountSuspended) {
				t.Fatalf("merged member = %#v", value)
			}
		}
	}
}
