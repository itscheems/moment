package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type archiveInteractionServices struct {
	posts        *app.PostService
	comments     *app.CommentService
	interactions *app.InteractionService
	owner        domain.User
}

func removeTestArchiveMember(members map[string][]byte, order []string, name string) []string {
	delete(members, name)
	result := make([]string, 0, len(order))
	for _, value := range order {
		if value != name {
			result = append(result, value)
		}
	}
	return result
}

func interactionServices(t *testing.T, db *sqlitestore.Database, runtime *runtimefs.Runtime) archiveInteractionServices {
	t.Helper()
	a, err := sqlitestore.NewAccountRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(a)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err := sqlitestore.NewPostRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sqlitestore.NewCommentRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := app.NewCommentService(c)
	if err != nil {
		t.Fatal(err)
	}
	i, err := sqlitestore.NewInteractionRepository(db, runtime.Locks, runtime.Current.ID)
	if err != nil {
		t.Fatal(err)
	}
	interactions, err := app.NewInteractionService(i, posts, comments)
	if err != nil {
		t.Fatal(err)
	}
	return archiveInteractionServices{posts: posts, comments: comments, interactions: interactions, owner: owner}
}

func archiveActor(user domain.User) context.Context {
	return app.WithContentActor(context.Background(), user)
}

func archiveMember(t *testing.T, fixture exportFixture, name, id string) domain.User {
	t.Helper()
	repo, err := sqlitestore.NewAccountRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repo, app.WithAccountIDGenerator(func() (string, error) { return id, nil }))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := accounts.SignIn(archiveActor(owner), app.ExternalIdentity{Provider: "google", ProviderUserID: name, DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	user, err := accounts.SetStatus(archiveActor(owner), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	user, err = accounts.UpdateProfile(archiveActor(user), user.ID, app.ProfileUpdate{Username: &name})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func capturedInteractions(t *testing.T, db *sqlitestore.Database) []sqlitestore.ArchiveInteraction {
	t.Helper()
	ctx := context.Background()
	snapshot, err := db.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	rows := make([]sqlitestore.ArchiveInteraction, 0)
	if err := snapshot.Interactions(ctx, func(row sqlitestore.ArchiveInteraction) error { rows = append(rows, row); return nil }); err != nil {
		t.Fatal(err)
	}
	if snapshot.Counts().Interactions != int64(len(rows)) {
		t.Fatal("snapshot interaction count disagrees with its records")
	}
	return rows
}

func TestInteractionSiteArchiveRestoresExactHistoryAndViewPositions(t *testing.T) {
	fixture := newExportFixture(t, false)
	services := interactionServices(t, fixture.database, fixture.runtime)
	cedar := archiveMember(t, fixture, "cedar", "11111111111111111111111111111111")
	maple := archiveMember(t, fixture, "maple", "22222222222222222222222222222222")
	post, err := services.posts.Create(archiveActor(services.owner), app.CreatePostInput{ContentMD: "Hello @cedar"})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := services.comments.Create(archiveActor(cedar), app.CreateCommentInput{EntryID: post.Post.Entry.ID, Author: cedar, ContentMD: "Reply @maple"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.interactions.Like(archiveActor(cedar), domain.ContentRef{Kind: domain.SubjectPost, ID: post.Post.Entry.ID}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := services.interactions.Like(archiveActor(services.owner), domain.ContentRef{Kind: domain.SubjectComment, ID: comment.Comment.Comment.ID}, true); err != nil {
		t.Fatal(err)
	}
	page, err := services.interactions.Notifications(archiveActor(cedar), app.NotificationListInput{Scope: "mentions"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.interactions.SeeNotifications(archiveActor(cedar), "mentions", page.ThroughCursor); err != nil {
		t.Fatal(err)
	}
	beforeState, err := services.interactions.NotificationState(archiveActor(cedar))
	if err != nil {
		t.Fatal(err)
	}
	if beforeState.NewCount != 1 || beforeState.MentionsNewCount != 0 {
		t.Fatalf("unexpected source state: %+v", beforeState)
	}
	mapleState, err := services.interactions.NotificationState(archiveActor(maple))
	if err != nil || mapleState.NewCount != 1 {
		t.Fatalf("explicit recipient state = %+v, %v", mapleState, err)
	}
	before := capturedInteractions(t, fixture.database)
	var backup bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &backup); err != nil {
		t.Fatal(err)
	}
	path := writeArchiveBytes(t, backup.Bytes())
	if err := ValidateFile(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), path, testAuthorization(7))
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Counts.Interactions != int64(len(before)) {
		t.Fatalf("inspection omitted interaction impact: %+v", inspection.Counts)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	runtime := &runtimefs.Runtime{Root: fixture.runtime.Root, Instance: fixture.runtime.Instance, Current: stagingGeneration(stageRoot, inspection.GenerationID), Locks: fixture.runtime.Locks}
	db, err := sqlitestore.Open(context.Background(), runtime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	after := capturedInteractions(t, db)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatalf("interaction graph changed on restore:\n%s\n%s", a, b)
	}
	restored := interactionServices(t, db, runtime)
	state, err := restored.interactions.NotificationState(archiveActor(cedar))
	if err != nil || state.NewCount != beforeState.NewCount || state.MentionsNewCount != beforeState.MentionsNewCount {
		t.Fatalf("restored state = %+v, %v", state, err)
	}
	if _, err := restored.interactions.Notifications(archiveActor(cedar), app.NotificationListInput{After: beforeState.ThroughCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("old generation cursor = %v", err)
	}
}

func TestInteractionPersonalImportPreservesInertActorsWithoutReplayingEvents(t *testing.T) {
	ctx := context.Background()
	source := newExportFixture(t, false)
	target := newExportFixture(t, false)
	const cedarID = "11111111111111111111111111111111"
	cedar := archiveMember(t, source, "cedar", cedarID)
	receiver := archiveMember(t, target, "maple", "22222222222222222222222222222222")
	localCedar := archiveMember(t, target, "cedar", cedarID)
	src := interactionServices(t, source.database, source.runtime)
	dst := interactionServices(t, target.database, target.runtime)
	post, err := src.posts.Create(archiveActor(src.owner), app.CreatePostInput{ContentMD: "Source @writer @cedar"})
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []domain.User{cedar, src.owner} {
		if _, err := src.interactions.Like(archiveActor(actor), domain.ContentRef{Kind: domain.SubjectPost, ID: post.Post.Entry.ID}, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := src.comments.Create(archiveActor(cedar), app.CreateCommentInput{EntryID: post.Post.Entry.ID, Author: cedar, ContentMD: "A reply"}); err != nil {
		t.Fatal(err)
	}
	localPost, err := dst.posts.Create(archiveActor(receiver), app.CreatePostInput{ContentMD: "Existing local content"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.interactions.Like(archiveActor(localCedar), domain.ContentRef{Kind: domain.SubjectPost, ID: localPost.Post.Entry.ID}, true); err != nil {
		t.Fatal(err)
	}
	before, err := dst.interactions.NotificationState(archiveActor(receiver))
	if err != nil || before.NewCount != 1 {
		t.Fatalf("local state = %+v, %v", before, err)
	}
	var personal bytes.Buffer
	if _, err := source.exporter.ExportAccount(ctx, &personal, src.owner.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := target.exporter.ImportAccount(ctx, bytes.NewReader(personal.Bytes()), receiver.ID, AccountImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.exporter.ImportAccount(ctx, bytes.NewReader(personal.Bytes()), receiver.ID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added.Interactions == 0 {
		t.Fatal("personal import omitted interactions")
	}
	state, err := dst.interactions.NotificationState(archiveActor(receiver))
	if err != nil || state.NewCount != before.NewCount {
		t.Fatalf("history changed the live badge: %+v, %v", state, err)
	}
	newer, err := dst.interactions.Notifications(archiveActor(receiver), app.NotificationListInput{After: before.ThroughCursor})
	if err != nil || len(newer.Items) != 0 {
		t.Fatalf("historical events replayed: %+v, %v", newer, err)
	}
	ref := domain.ContentRef{Kind: domain.SubjectPost, ID: post.Post.Entry.ID}
	like, err := dst.interactions.LikeStatus(archiveActor(localCedar), ref, app.AccountViewer(localCedar.ID))
	if err != nil || like.Count != 2 || like.LikedByMe {
		t.Fatalf("external actor acquired local like authority: %+v, %v", like, err)
	}
	like, err = dst.interactions.LikeStatus(archiveActor(receiver), ref, app.AccountViewer(receiver.ID))
	if err != nil || !like.LikedByMe {
		t.Fatalf("own like was not remapped: %+v, %v", like, err)
	}
	reply, err := dst.interactions.ReplyContext(archiveActor(localCedar), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Participants) != 1 || reply.Participants[0].UserID != receiver.ID {
		t.Fatalf("external participants acquired authority: %+v", reply)
	}
	repeated, err := target.exporter.ImportAccount(ctx, bytes.NewReader(personal.Bytes()), receiver.ID, AccountImportOptions{ExpectedSHA: plan.ArchiveSHA256})
	if err != nil || repeated.Added.Interactions != 0 {
		t.Fatalf("repeated import = %+v, %v", repeated, err)
	}
	var backup bytes.Buffer
	if _, err := target.exporter.Export(ctx, &backup); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFile(writeArchiveBytes(t, backup.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestInteractionMergeKeepsLocalUnreadAndOrdersImportedHistoryByEventTime(t *testing.T) {
	ctx := context.Background()
	local, incoming := newExportFixture(t, false), newExportFixture(t, false)
	cedar := archiveMember(t, local, "cedar", "11111111111111111111111111111111")
	archiveMember(t, incoming, "cedar", cedar.ID)
	create := func(f exportFixture, at int64) domain.ID {
		repo, err := sqlitestore.NewPostRepository(f.database, f.runtime.Locks)
		if err != nil {
			t.Fatal(err)
		}
		posts, err := app.NewPostService(repo, app.WithPostClock(func() time.Time { return time.Unix(at, 0) }))
		if err != nil {
			t.Fatal(err)
		}
		svc := interactionServices(t, f.database, f.runtime)
		result, err := posts.Create(archiveActor(svc.owner), app.CreatePostInput{ContentMD: "Message @cedar"})
		if err != nil {
			t.Fatal(err)
		}
		return result.Post.Entry.ID
	}
	localID, incomingID := create(local, 2000), create(incoming, 1000)
	src := interactionServices(t, incoming.database, incoming.runtime)
	if _, err := src.interactions.Like(archiveActor(cedar), domain.ContentRef{Kind: domain.SubjectPost, ID: incomingID}, true); err != nil {
		t.Fatal(err)
	}
	left, err := ReadPortableAuthority(ctx, local.database)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ReadPortableAuthority(ctx, incoming.database)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(a, b PortableAuthority) ResolvedMerge {
		plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("c"), BaseGeneration: local.runtime.Current.ID, ExpiresAt: time.Now().Add(time.Hour), Local: a, Incoming: b})
		if err != nil {
			t.Fatal(err)
		}
		result, err := ResolvePortableMerge(plan, plan.DefaultSelection, a, b, MergeResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	resolved := resolve(left, right)
	again := resolve(resolved.Authority, right)
	if len(again.Authority.Interactions) != len(resolved.Authority.Interactions) {
		t.Fatal("repeated merge duplicated history")
	}
	generation := stagingGeneration(filepath.Join(t.TempDir(), "merged"), "merged-test")
	if err := os.MkdirAll(generation.OriginalsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := rebuildResolvedMergeGeneration(ctx, local.runtime, generation, resolved, local.runtime.Current.Root, incoming.runtime.Current.Root); err != nil {
		t.Fatal(err)
	}
	runtime := &runtimefs.Runtime{Root: local.runtime.Root, Instance: local.runtime.Instance, Current: generation, Locks: local.runtime.Locks}
	db, err := sqlitestore.Open(ctx, runtime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := interactionServices(t, db, runtime)
	state, err := svc.interactions.NotificationState(archiveActor(cedar))
	if err != nil || state.NewCount != 1 {
		t.Fatalf("local unread changed: %+v %v", state, err)
	}
	first, err := svc.interactions.Notifications(archiveActor(cedar), app.NotificationListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Record.Subject.ID != localID || first.Items[0].Seen || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	second, err := svc.interactions.Notifications(archiveActor(cedar), app.NotificationListInput{Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Record.Subject.ID != incomingID || !second.Items[0].Seen {
		t.Fatalf("historical page: %+v", second)
	}
	like, err := svc.interactions.LikeStatus(archiveActor(cedar), domain.ContentRef{Kind: domain.SubjectPost, ID: incomingID}, app.AccountViewer(cedar.ID))
	if err != nil || !like.LikedByMe || like.Count != 1 {
		t.Fatalf("merged like: %+v %v", like, err)
	}
}

func TestInteractionArchiveVersionAndInvalidReferences(t *testing.T) {
	f := newExportFixture(t, false)
	for _, name := range []string{"legacy-v5", "missing-current-member", "dangling-actor", "duplicate-relation"} {
		t.Run(name, func(t *testing.T) {
			members, order := readArchive(t, exportFixtureBytes(t, f))
			valid := false
			switch name {
			case "legacy-v5":
				var m map[string]any
				if err := json.Unmarshal(members["manifest.json"], &m); err != nil {
					t.Fatal(err)
				}
				m["format_version"] = 5
				members["manifest.json"] = append(mustMarshalTest(t, m), '\n')
				members["README.txt"] = []byte(strings.Replace(readme, "v6", "v5", 1))
				order = removeTestArchiveMember(members, order, interactionMember)
				valid = true
			case "missing-current-member":
				order = removeTestArchiveMember(members, order, interactionMember)
			default:
				actor := fixtureAccountID
				if name == "dangling-actor" {
					actor = strings.Repeat("f", 32)
				}
				row := sqlitestore.ArchiveInteraction{Kind: "post_like", SubjectID: entryID, UserID: &actor, CreatedAtMS: 1}
				members[interactionMember] = append(mustMarshalTest(t, row), '\n')
				count := 1
				if name == "duplicate-relation" {
					members[interactionMember] = append(members[interactionMember], members[interactionMember]...)
					count = 2
				}
				var m map[string]any
				if err := json.Unmarshal(members["manifest.json"], &m); err != nil {
					t.Fatal(err)
				}
				m["counts"].(map[string]any)["interactions"] = count
				members["manifest.json"] = append(mustMarshalTest(t, m), '\n')
			}
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
			path := filepath.Join(t.TempDir(), "test.zip")
			writeArchive(t, path, members, order)
			err := ValidateFile(path)
			if valid {
				if err != nil {
					t.Fatal(err)
				}
				db := stageArchive(t, f, path)
				if len(capturedInteractions(t, db)) != 0 {
					t.Fatal("legacy import invented activity")
				}
			} else if err == nil {
				t.Fatal("invalid interactions accepted")
			}
		})
	}
}
