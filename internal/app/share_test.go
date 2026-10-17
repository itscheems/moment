package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediacontract"
)

const shareTestPostID = "01K2ABCDEF0123456789ABCDE0"

type shareSiteStub struct {
	settings SiteSettings
	err      error
}

func (stub shareSiteStub) Get(context.Context) (SiteSettings, error) {
	return stub.settings, stub.err
}

type sharePostStub struct {
	post Post
	err  error
}

func (stub sharePostStub) Get(context.Context, domain.ID, Viewer) (Post, error) {
	return stub.post, stub.err
}

func TestShareMetadataUsesPortableBaseAndManagedPreview(t *testing.T) {
	base := "https://example.com/journal"
	avatar := "01K2ABCDEF0123456789ABCDE1"
	postID := mustShareID(t, shareTestPostID)
	mediaID := mustShareID(t, "01K2ABCDEF0123456789ABCDE2")
	service, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{Title: "乔乔 <家>", Bio: "沿着\u0000日常留下坐标", PublicBaseURL: &base, AvatarMediaID: &avatar}},
		sharePostStub{post: Post{
			Entry: domain.Entry{ID: postID, Kind: domain.KindMoment, Visibility: domain.VisibilityEveryone, ContentMD: domain.MarkdownBody("# 第一行 **记忆**\n\n[可见文字](https://private.example/path)"), CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1), PublishedAt: time.UnixMilli(1)},
			Media: []MediaUsageProjection{{MediaID: mediaID, Position: 0, MIMEType: "image/jpeg", Width: 32, Height: 32}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	metadata, err := service.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "第一行 记忆 · 乔乔 <家>" || metadata.Description != "第一行 记忆 可见文字" || metadata.Type != "article" || !metadata.PostFound || metadata.NoIndex {
		t.Fatalf("post metadata = %#v", metadata)
	}
	if metadata.CanonicalURL != "https://example.com/journal/m/"+shareTestPostID || metadata.ImageURL != "https://example.com/journal/api/v1/media/"+mediaID.String()+"/preview?v="+mediacontract.RecipeVersion {
		t.Fatalf("post URLs = %#v", metadata)
	}

	search, err := service.Page(context.Background(), SharePageSearch)
	if err != nil {
		t.Fatal(err)
	}
	if search.Title != "搜索 · 乔乔 <家>" || search.CanonicalURL != "https://example.com/journal/search" || search.ImageURL != "https://example.com/journal/api/v1/media/"+avatar+"/preview?v="+mediacontract.RecipeVersion {
		t.Fatalf("search metadata = %#v", search)
	}
}

func TestShareMetadataFallsBackForMissingPostsAndMissingBase(t *testing.T) {
	postID := mustShareID(t, shareTestPostID)
	missing := domain.NewError(domain.CodePostNotFound, "missing", nil)
	service, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{Title: "", Bio: ""}},
		sharePostStub{err: missing},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := service.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Moment" || metadata.Description != "记下此刻，找回后来。" || metadata.CanonicalURL != "" || metadata.ImageURL != "" || metadata.PostFound || !metadata.NoIndex {
		t.Fatalf("fallback metadata = %#v", metadata)
	}

	base := "https://example.com/journal"
	unreadable, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{Title: "Portable", PublicBaseURL: &base}},
		sharePostStub{err: errors.New("database unavailable")},
	)
	if err != nil {
		t.Fatal(err)
	}
	unreadableMetadata, err := unreadable.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if unreadableMetadata.Title != "Portable" || unreadableMetadata.CanonicalURL != base || !unreadableMetadata.NoIndex || unreadableMetadata.PostFound {
		t.Fatalf("unreadable post metadata = %#v", unreadableMetadata)
	}

	failed, err := NewShareMetadataService(shareSiteStub{err: errors.New("storage failed")}, sharePostStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.Page(context.Background(), SharePageTimeline); err == nil {
		t.Fatal("site read failure was hidden")
	}
}

func TestShareMetadataRejectsInvalidPortableIdentityAndBoundsUnicode(t *testing.T) {
	invalidBase := "https://user@example.com/journal?private=1#fragment"
	invalidAvatar := "../../private"
	postID := mustShareID(t, shareTestPostID)
	service, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{
			Title:         strings.Repeat("记", 120),
			Bio:           "简介",
			PublicBaseURL: &invalidBase,
			AvatarMediaID: &invalidAvatar,
		}},
		sharePostStub{post: Post{Entry: domain.Entry{
			ID:          postID,
			Kind:        domain.KindMoment,
			Visibility:  domain.VisibilityEveryone,
			ContentMD:   domain.MarkdownBody(strings.Repeat("界", 240)),
			CreatedAt:   time.UnixMilli(1),
			UpdatedAt:   time.UnixMilli(1),
			PublishedAt: time.UnixMilli(1),
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := service.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.CanonicalURL != "" || metadata.ImageURL != "" {
		t.Fatalf("invalid portable identity produced absolute URLs: %#v", metadata)
	}
	if len([]rune(metadata.Title)) > shareTitleLimit || len([]rune(metadata.Description)) > shareDescriptionLimit || !strings.HasSuffix(metadata.Description, "…") {
		t.Fatalf("metadata was not bounded by Unicode code points: %#v", metadata)
	}
}

func mustShareID(t *testing.T, value string) domain.ID {
	t.Helper()
	id, err := domain.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Share metadata is read on behalf of whoever will follow the link, so the
// post is fetched as a visitor whatever credential loaded the page.
func TestShareMetadataReadsThePostAsAVisitor(t *testing.T) {
	postID := mustShareID(t, shareTestPostID)
	var seen *Viewer
	service, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{Title: "Site"}},
		sharePostRecorder{seen: &seen},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := service.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.UserID() != "" {
		t.Fatalf("post read with viewer %v", seen)
	}
	if metadata.PostFound || !metadata.NoIndex {
		t.Fatalf("hidden post produced %#v", metadata)
	}
}

type sharePostRecorder struct{ seen **Viewer }

func (stub sharePostRecorder) Get(_ context.Context, id domain.ID, viewer Viewer) (Post, error) {
	*stub.seen = &viewer
	return Post{}, postNotFound(id)
}

// A moment has no title, so share metadata guesses one from its first visible
// line. An article has one already, and using it is most of the reason it has
// one.
func TestShareMetadataUsesTheTitleAndExcerptAnArticleAlreadyHas(t *testing.T) {
	base := "https://example.com"
	postID := mustShareID(t, shareTestPostID)
	body, err := domain.ParseArticleDocument([]byte(
		`{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"正文的第一段会成为摘要"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("长安的荔枝", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewShareMetadataService(
		shareSiteStub{settings: SiteSettings{Title: "乔乔", PublicBaseURL: &base}},
		sharePostStub{post: Post{
			Entry: domain.Entry{
				ID: postID, Kind: domain.KindArticle, Visibility: domain.VisibilityEveryone,
				CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1), PublishedAt: time.UnixMilli(1),
			},
			Article:     &article,
			ArticleCard: &ArticleCard{Title: article.Title, Excerpt: article.Excerpt()},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	metadata, err := service.Post(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "长安的荔枝 · 乔乔" {
		t.Fatalf("title = %q", metadata.Title)
	}
	if metadata.Description != "正文的第一段会成为摘要" {
		t.Fatalf("description = %q", metadata.Description)
	}
	if !metadata.PostFound || metadata.NoIndex {
		t.Fatalf("metadata = %#v", metadata)
	}
}
