package sqlite

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

type interactionFixture struct {
	memberIndex int
	db          *Database
	runtime     *runtimefs.Runtime
	accounts    *app.AccountService
	posts       *app.PostService
	comments    *app.CommentService
	service     *app.InteractionService
	owner       domain.User
}

func newInteractionFixture(t *testing.T) *interactionFixture {
	t.Helper()
	db, runtime := openTestDatabase(t, Options{})
	a, err := NewAccountRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(a)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.ProfileAccount(testAccountContext())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPostRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCommentRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := app.NewCommentService(c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewInteractionRepository(db, runtime.Locks, runtime.Current.ID)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewInteractionService(r, posts, comments)
	if err != nil {
		t.Fatal(err)
	}
	return &interactionFixture{db: db, runtime: runtime, accounts: accounts, posts: posts, comments: comments, service: service, owner: owner}
}

func activityContext(user domain.User) context.Context {
	return app.WithContentActor(context.Background(), user)
}

func (f *interactionFixture) member(t *testing.T, name string) domain.User {
	t.Helper()
	ctx := activityContext(f.owner)
	f.memberIndex++
	outcome, err := f.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: name + strconv.Itoa(f.memberIndex), DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	user, err := f.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	user, err = f.accounts.UpdateProfile(activityContext(user), user.ID, app.ProfileUpdate{Username: &name})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func (f *interactionFixture) post(t *testing.T, body string, visibility domain.Visibility) app.Post {
	t.Helper()
	result, err := f.posts.Create(activityContext(f.owner), app.CreatePostInput{ContentMD: body, Visibility: visibility})
	if err != nil {
		t.Fatal(err)
	}
	return result.Post
}

func (f *interactionFixture) page(t *testing.T, user domain.User, scope string) app.NotificationPage {
	t.Helper()
	page, err := f.service.Notifications(activityContext(user), app.NotificationListInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestInteractionLikesAreAtomicAndIdempotentUnderConcurrency(t *testing.T) {
	f := newInteractionFixture(t)
	member := f.member(t, "cedar")
	post := f.post(t, "A public entry", domain.VisibilityEveryone)
	ref := domain.ContentRef{Kind: domain.SubjectPost, ID: post.Entry.ID}
	errors := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := f.service.Like(activityContext(member), ref, true)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := f.service.LikeStatus(context.Background(), ref, app.PublicViewer())
	if err != nil || state.Count != 1 || state.LikedByMe {
		t.Fatalf("public state = %+v, %v", state, err)
	}
	state, err = f.service.LikeStatus(activityContext(member), ref, app.AccountViewer(member.ID))
	if err != nil || !state.LikedByMe {
		t.Fatalf("account state = %+v, %v", state, err)
	}
	if page := f.page(t, f.owner, "all"); len(page.Items) != 1 || page.NewCount != 1 {
		t.Fatalf("notification page = %+v", page)
	}
	for range 2 {
		if state, err = f.service.Like(activityContext(member), ref, false); err != nil || state.Count != 0 {
			t.Fatalf("unlike = %+v, %v", state, err)
		}
	}
	if _, err = f.service.Like(activityContext(member), ref, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Like(activityContext(f.owner), ref, true); err != nil {
		t.Fatal(err)
	}
	if page := f.page(t, f.owner, "all"); len(page.Items) != 2 {
		t.Fatalf("re-like and self-like produced %d events", len(page.Items))
	}
	if _, err = f.service.Like(context.Background(), ref, true); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("anonymous like = %v", err)
	}
}

func TestInteractionRepliesInheritSelectedPeopleAndMergeMentionReasons(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	maple := f.member(t, "maple")
	birch := f.member(t, "birch")
	post := f.post(t, "A conversation", domain.VisibilityEveryone)
	root, err := f.comments.Create(activityContext(cedar), app.CreateCommentInput{EntryID: post.Entry.ID, Author: cedar, ContentMD: "Root"})
	if err != nil {
		t.Fatal(err)
	}
	rootID := root.Comment.Comment.ID
	reply, err := f.comments.Create(activityContext(maple), app.CreateCommentInput{EntryID: post.Entry.ID, ParentID: &rootID, Author: maple, ContentMD: "Hello @cedar", ExcludedReplyUserIDs: []string{f.owner.ID}, IdempotencyKey: "reply-once"})
	if err != nil {
		t.Fatal(err)
	}
	page := f.page(t, cedar, "all")
	if len(page.Items) != 1 || page.Items[0].Record.Reasons != (domain.ReasonReply|domain.ReasonMention) {
		t.Fatalf("merged reply = %+v", page)
	}
	if page.Items[0].Record.ReplyToCommentID == nil || *page.Items[0].Record.ReplyToCommentID != rootID {
		t.Fatal("event lost the actual reply target")
	}
	if page := f.page(t, f.owner, "all"); len(page.Items) != 1 {
		t.Fatalf("excluded owner received %d events", len(page.Items))
	}
	context, err := f.service.ReplyContext(activityContext(birch), domain.ContentRef{Kind: domain.SubjectComment, ID: reply.Comment.Comment.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, person := range context.Participants {
		if person.UserID == f.owner.ID {
			t.Fatal("excluded participant reappeared in the next reply")
		}
	}
	replyID := reply.Comment.Comment.ID
	_, err = f.comments.Create(activityContext(birch), app.CreateCommentInput{EntryID: post.Entry.ID, ParentID: &rootID, ReplyToCommentID: &replyID, Author: birch, ContentMD: "Further reply"})
	if err != nil {
		t.Fatal(err)
	}
	if page := f.page(t, f.owner, "all"); len(page.Items) != 1 {
		t.Fatal("reply broadcast to a removed participant")
	}
	if page := f.page(t, maple, "all"); len(page.Items) != 1 || page.Items[0].Record.Reasons != domain.ReasonReply {
		t.Fatalf("direct recipient = %+v", page)
	}
	before := f.page(t, cedar, "all")
	_, err = f.comments.Create(activityContext(maple), app.CreateCommentInput{EntryID: post.Entry.ID, ParentID: &rootID, Author: maple, ContentMD: "Invalid", ExcludedReplyUserIDs: []string{cedar.ID}})
	if !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("required recipient removal = %v", err)
	}
	if len(f.page(t, cedar, "all").Items) != len(before.Items) {
		t.Fatal("invalid reply left an event")
	}
	assertCount(t, f.db.read, "SELECT count(*) FROM comments", 3)
	replayed, err := f.comments.Create(activityContext(maple), app.CreateCommentInput{EntryID: post.Entry.ID, ParentID: &rootID, Author: maple, ContentMD: "Hello @cedar", ExcludedReplyUserIDs: []string{f.owner.ID}, IdempotencyKey: "reply-once"})
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if len(f.page(t, cedar, "all").Items) != len(before.Items) {
		t.Fatal("replayed comment notified twice")
	}
}

func TestInteractionMentionsUseCurrentBodiesAndCurrentPermissions(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	other := f.member(t, "maple")
	post := f.post(t, "Private @cedar", domain.VisibilityOnlyMe)
	if page := f.page(t, cedar, "all"); len(page.Items) != 0 {
		t.Fatal("private mention delivered")
	}
	ref := domain.ContentRef{Kind: domain.SubjectPost, ID: post.Entry.ID}
	if _, err := f.service.Like(activityContext(cedar), ref, true); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("hidden target = %v", err)
	}
	public := domain.VisibilityEveryone
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, PreserveContent: true, Visibility: &public}); err != nil {
		t.Fatal(err)
	}
	page := f.page(t, cedar, "all")
	if len(page.Items) != 1 || !page.Items[0].Available {
		t.Fatalf("first public mention = %+v", page)
	}
	id := page.Items[0].Record.ID.String()
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, ContentMD: "Latest text @cedar"}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.service.Notification(activityContext(cedar), id)
	if err != nil || detail.Post == nil || detail.Post.Entry.ContentMD == nil || *detail.Post.Entry.ContentMD != "Latest text @cedar" {
		t.Fatalf("current body = %+v, %v", detail, err)
	}
	if len(f.page(t, cedar, "all").Items) != 1 {
		t.Fatal("unrelated edit repeated mention")
	}
	if _, err := f.service.Notification(activityContext(other), id); !domain.HasCode(err, domain.CodeNotificationNotFound) {
		t.Fatalf("cross-account event read = %v", err)
	}
	private := domain.VisibilityOnlyMe
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, PreserveContent: true, Visibility: &private}); err != nil {
		t.Fatal(err)
	}
	page = f.page(t, cedar, "all")
	if len(page.Items) != 1 || page.Items[0].Available || page.Items[0].Excerpt != "" || page.NewCount != 0 {
		t.Fatalf("private preview leaked: %+v", page)
	}
	detail, err = f.service.Notification(activityContext(cedar), id)
	if err != nil || detail.Available || detail.Post != nil || detail.Excerpt != "" {
		t.Fatalf("private detail = %+v, %v", detail, err)
	}
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, PreserveContent: true, Visibility: &public}); err != nil {
		t.Fatal(err)
	}
	if len(f.page(t, cedar, "all").Items) != 1 {
		t.Fatal("visibility toggle repeated old mention")
	}
	if _, err := f.posts.Delete(activityContext(f.owner), post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	detail, err = f.service.Notification(activityContext(cedar), id)
	if err != nil || detail.Available || detail.Post != nil {
		t.Fatalf("deleted detail = %+v, %v", detail, err)
	}
}

func TestInteractionSeenScopesAndSnapshotBoundaryDoNotConsumeNewActivity(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	maple := f.member(t, "maple")
	birch := f.member(t, "birch")
	post := f.post(t, "A public entry", domain.VisibilityEveryone)
	ref := domain.ContentRef{Kind: domain.SubjectPost, ID: post.Entry.ID}
	if _, err := f.service.Like(activityContext(cedar), ref, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.comments.Create(activityContext(maple), app.CreateCommentInput{EntryID: post.Entry.ID, Author: maple, ContentMD: "A comment"}); err != nil {
		t.Fatal(err)
	}
	all := f.page(t, f.owner, "all")
	mentions := f.page(t, f.owner, "mentions")
	if all.NewCount != 2 || len(mentions.Items) != 1 {
		t.Fatalf("all=%+v mentions=%+v", all, mentions)
	}
	state, err := f.service.SeeNotifications(activityContext(f.owner), "mentions", mentions.ThroughCursor)
	if err != nil || state.NewCount != 1 || state.MentionsNewCount != 0 {
		t.Fatalf("mentions acknowledgement = %+v, %v", state, err)
	}
	if _, err := f.service.SeeNotifications(activityContext(cedar), "all", all.ThroughCursor); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("foreign cursor = %v", err)
	}
	if _, err := f.service.SeeNotifications(activityContext(f.owner), "all", mentions.ThroughCursor); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("wrong-view cursor = %v", err)
	}
	if _, err := f.service.Like(activityContext(birch), ref, true); err != nil {
		t.Fatal(err)
	}
	state, err = f.service.SeeNotifications(activityContext(f.owner), "all", all.ThroughCursor)
	if err != nil || state.NewCount != 1 {
		t.Fatalf("new event was consumed: %+v, %v", state, err)
	}
	newer, err := f.service.Notifications(activityContext(f.owner), app.NotificationListInput{After: all.ThroughCursor})
	if err != nil || len(newer.Items) != 1 {
		t.Fatalf("incremental events = %+v, %v", newer, err)
	}
	latest := f.page(t, f.owner, "all")
	if _, err := f.service.SeeNotifications(activityContext(f.owner), "all", latest.ThroughCursor); err != nil {
		t.Fatal(err)
	}
	if state, err := f.service.SeeNotifications(activityContext(f.owner), "all", all.ThroughCursor); err != nil || state.NewCount != 0 {
		t.Fatalf("older acknowledgement moved progress back: %+v, %v", state, err)
	}
}

func TestInteractionSinkFailureRollsBackContentLikesAndRetryRecord(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	post := f.post(t, "A public entry", domain.VisibilityEveryone)
	mustWrite(t, f.db, `CREATE TRIGGER fail_notifications BEFORE INSERT ON notifications BEGIN SELECT RAISE(ABORT,'notification storage unavailable'); END`)
	if _, err := f.service.Like(activityContext(cedar), domain.ContentRef{Kind: domain.SubjectPost, ID: post.Entry.ID}, true); err == nil {
		t.Fatal("like succeeded despite notification failure")
	}
	assertCount(t, f.db.read, "SELECT count(*) FROM post_likes", 0)
	input := app.CreateCommentInput{EntryID: post.Entry.ID, Author: cedar, ContentMD: "Retry safely", IdempotencyKey: "retry-after-failure"}
	if _, err := f.comments.Create(activityContext(cedar), input); err == nil {
		t.Fatal("comment succeeded despite notification failure")
	}
	assertCount(t, f.db.read, "SELECT count(*) FROM comments", 0)
	assertCount(t, f.db.read, "SELECT count(*) FROM idempotency_keys", 0)
	if _, err := f.posts.Create(activityContext(f.owner), app.CreatePostInput{ContentMD: "Notify @cedar"}); err == nil {
		t.Fatal("post succeeded despite notification failure")
	}
	assertCount(t, f.db.read, "SELECT count(*) FROM entries", 1)
	mustWrite(t, f.db, "DROP TRIGGER fail_notifications")
	created, err := f.comments.Create(activityContext(cedar), input)
	if err != nil || created.Replayed {
		t.Fatalf("retry = %+v, %v", created, err)
	}
	assertCount(t, f.db.read, "SELECT count(*) FROM notifications", 1)
}

func TestInteractionMentionBindingsSurviveRenameAndDoNotBindUnchangedUnknownText(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	post := f.post(t, "Hello @cedar and @willow", domain.VisibilityEveryone)
	renamed := "cedar_renamed"
	var err error
	cedar, err = f.accounts.UpdateProfile(activityContext(cedar), cedar.ID, app.ProfileUpdate{Username: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	willow := f.member(t, "willow")
	replacement := f.member(t, "cedar")
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, ContentMD: "Hello @cedar and @willow!"}); err != nil {
		t.Fatal(err)
	}
	if len(f.page(t, willow, "all").Items) != 0 || len(f.page(t, replacement, "all").Items) != 0 {
		t.Fatal("unchanged text was rebound to a new account")
	}
	context, err := f.service.ReplyContext(activityContext(willow), domain.ContentRef{Kind: domain.SubjectPost, ID: post.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, person := range context.Participants {
		if person.UserID == replacement.ID {
			t.Fatal("handle reuse changed historical recipient")
		}
		if person.UserID == cedar.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("renamed recipient lost its binding")
	}
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, ContentMD: "Removed handles"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.posts.Update(activityContext(f.owner), app.UpdatePostInput{ID: post.Entry.ID, ContentMD: "New @willow"}); err != nil {
		t.Fatal(err)
	}
	if len(f.page(t, willow, "all").Items) != 1 {
		t.Fatal("newly added mention did not notify")
	}
}

func TestInteractionArticleMentionReturnsStructuredLatestBody(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	draft, err := f.posts.SaveArticleDraft(activityContext(f.owner), app.SaveArticleDraftInput{Title: "An article", BodyJSON: draftBodyJSON("Article body @cedar")})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.page(t, cedar, "all").Items) != 0 {
		t.Fatal("draft notified a recipient")
	}
	post, err := f.posts.PublishArticleDraft(activityContext(f.owner), app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	page := f.page(t, cedar, "all")
	if len(page.Items) != 1 || page.Items[0].Record.PostKind != domain.KindArticle {
		t.Fatalf("article event = %+v", page)
	}
	detail, err := f.service.Notification(activityContext(cedar), page.Items[0].Record.ID.String())
	if err != nil || detail.Post == nil || detail.Post.Article == nil || detail.Post.Entry.ID != post.Entry.ID {
		t.Fatalf("article detail = %+v, %v", detail, err)
	}
	if got := domain.ArticleVisibleText(detail.Post.Article.Body); got != "Article body @cedar" {
		t.Fatalf("article body = %q", got)
	}
}

func TestInteractionCommentLikesPersistAndDeletedSourcesDoNotBlockIncrementalReads(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	post := f.post(t, "A public entry", domain.VisibilityEveryone)
	comment, err := f.comments.Create(activityContext(cedar), app.CreateCommentInput{EntryID: post.Entry.ID, Author: cedar, ContentMD: "A comment"})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.ContentRef{Kind: domain.SubjectComment, ID: comment.Comment.Comment.ID}
	start := f.page(t, cedar, "all").ThroughCursor
	for index := range 5 {
		if _, err := f.service.Like(activityContext(f.owner), ref, true); err != nil {
			t.Fatal(err)
		}
		if index < 4 {
			if _, err := f.service.Like(activityContext(f.owner), ref, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.service.Like(activityContext(cedar), ref, true); err != nil {
		t.Fatal(err)
	}
	if state, err := f.service.LikeStatus(context.Background(), ref, app.PublicViewer()); err != nil || state.Count != 2 || state.LikedByMe {
		t.Fatalf("comment likes = %+v, %v", state, err)
	}
	if len(f.page(t, cedar, "all").Items) != 5 {
		t.Fatal("self-like generated an extra event")
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), f.runtime, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	postRepo, err := NewPostRepository(reopened, f.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(postRepo)
	if err != nil {
		t.Fatal(err)
	}
	commentRepo, err := NewCommentRepository(reopened, f.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := app.NewCommentService(commentRepo)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewInteractionRepository(reopened, f.runtime.Locks, f.runtime.Current.ID)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewInteractionService(repository, posts, comments)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.LikeStatus(activityContext(f.owner), ref, app.AccountViewer(f.owner.ID))
	if err != nil || state.Count != 2 || !state.LikedByMe {
		t.Fatalf("reopened like state = %+v, %v", state, err)
	}
	if _, err := comments.DeleteBy(activityContext(f.owner), ref.ID, f.owner, false); err != nil {
		t.Fatal(err)
	}
	count, previous := 0, int64(0)
	for {
		page, err := service.Notifications(activityContext(cedar), app.NotificationListInput{After: start, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page.NewCount != 0 {
			t.Fatal("deleted content counted as available new activity")
		}
		for _, item := range page.Items {
			if item.Available || item.Excerpt != "" || item.Record.Sequence <= previous {
				t.Fatalf("invalid incremental item: %+v", item)
			}
			previous = item.Record.Sequence
			count++
		}
		if page.NextCursor == "" {
			break
		}
		start = page.NextCursor
	}
	if count != 5 {
		t.Fatalf("incremental read stopped at %d events", count)
	}
}

func TestInteractionBoundMentionReadDoesNotFollowReusedHandles(t *testing.T) {
	f := newInteractionFixture(t)
	cedar := f.member(t, "cedar")
	post := f.post(t, "Hello @cedar @unknown_handle", domain.VisibilityEveryone)
	name := "maple"
	if _, err := f.accounts.UpdateProfile(activityContext(cedar), cedar.ID, app.ProfileUpdate{Username: &name}); err != nil {
		t.Fatal(err)
	}
	f.member(t, "cedar")
	values, err := f.service.BoundMentions(context.Background(), domain.ContentRef{Kind: "post", ID: post.Entry.ID}, app.PublicViewer())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Handle != "cedar" || values[0].Account == nil || values[0].Account.UserID != cedar.ID || values[0].Account.Username != "maple" || values[1].Account != nil {
		t.Fatalf("saved identity drifted: %+v", values)
	}
	hidden := f.post(t, "@maple", domain.VisibilityOnlyMe)
	if _, err := f.service.BoundMentions(context.Background(), domain.ContentRef{Kind: "post", ID: hidden.Entry.ID}, app.PublicViewer()); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("private bindings leaked: %v", err)
	}
}
