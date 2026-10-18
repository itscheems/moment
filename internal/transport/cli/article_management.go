package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerArticleManagementCommands(application *Application) {
	application.MustRegisterLeaf(LeafSpec{Path: []string{"article", "list"}, Short: "List your published articles", Long: "List original published articles belonging to the current account, without full bodies or repost projections.", Inputs: "An optional opaque --cursor continues the previous page.", Output: "Article cards and continuation metadata.", Errors: "Only an active account can inspect its inventory.", Example: "moment --as writer article list --url http://127.0.0.1:18080 --json", Capability: CapabilityBoth, Flags: func(flags *pflag.FlagSet) { flags.String("cursor", "", "opaque continuation cursor") }, Run: runArticleInventory})
	application.MustRegisterLeaf(LeafSpec{Path: []string{"article", "get"}, ArgsUsage: "<post-id>", Short: "Read your article for management", Long: "Read one article owned by the current account, including a withdrawn article. Other accounts have no override.", Inputs: "The article id.", Output: "The complete article and author.", Errors: "Missing articles and unauthorized account identities are refused.", Example: "moment --as writer article get 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json", Args: cobra.ExactArgs(1), Capability: CapabilityBoth, Run: runManagedArticleGet})
	for _, verb := range []string{"unpublish", "delete"} {
		action := verb
		application.MustRegisterLeaf(LeafSpec{Path: []string{"article", verb}, ArgsUsage: "<post-id>", Short: verb + " your article", Long: "Unpublish retains the article id, publication time and comments, moving its working copy into drafts. Delete permanently removes the article, linked draft and comments.", Inputs: "One article id owned by the current account.", Output: "The resulting draft, or the deleted article.", Errors: "Quota failures leave public content unchanged; another author's article cannot be managed.", Safety: "Use --dry-run to inspect. --yes confirms the change and never bypasses author checks.", Example: "moment --as writer article " + verb + " 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --dry-run --json", Args: cobra.ExactArgs(1), Capability: CapabilityBoth, Policy: SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: verb + " this article?"}, Run: func(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
			return runArticleManagementMutation(ctx, inv, input, action)
		}})
	}
}

func managedArticleByTransport(ctx context.Context, inv Invocation, id domain.ID) (postdto.Post, map[string]any, error) {
	if inv.Mode() == ModeDirect {
		service, _, db, err := openDirectPostRead(ctx, inv)
		if err != nil {
			return postdto.Post{}, nil, err
		}
		defer func() { _ = db.Close() }()
		post, err := service.ManagedArticle(ctx, id)
		return postdto.FromPost(post), nil, postCLIError(err)
	}
	var post postdto.Post
	meta, status, err := postAPIRequest(ctx, inv, http.MethodGet, "/api/v1/articles/"+id.String(), nil, nil, "", true, &post)
	if err == nil && status != http.StatusOK {
		err = invalidAPIResponse()
	}
	return post, meta, err
}
func runManagedArticleGet(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	post, meta, err := managedArticleByTransport(ctx, inv, id)
	if err != nil {
		return Result{}, err
	}
	return postResult("Article", post, meta), nil
}
func runArticleInventory(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
	cursor, _ := input.String("cursor")
	var page postdto.Page
	var meta map[string]any
	if inv.Mode() == ModeDirect {
		service, _, db, err := openDirectPostRead(ctx, inv)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = db.Close() }()
		result, err := service.PublishedArticles(ctx, app.ListPostsInput{Cursor: cursor, Limit: 20})
		if err != nil {
			return Result{}, postCLIError(err)
		}
		page = postdto.FromPage(result)
		meta = map[string]any{"next_cursor": postdto.NextCursor(result), "prev_cursor": postdto.PrevCursor(result), "refresh_cursor": postdto.RefreshCursor(result)}
	} else {
		var status int
		var err error
		meta, status, err = postAPIRequest(ctx, inv, http.MethodGet, "/api/v1/articles", url.Values{"cursor": []string{cursor}}, nil, "", true, &page)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
	}
	return Result{Data: page, Meta: meta, Text: "Published articles"}, nil
}
func runArticleManagementMutation(ctx context.Context, inv Invocation, input CommandInput, action string) (Result, error) {
	id, err := postID(input.Args()[0])
	if err != nil {
		return Result{}, postCLIError(err)
	}
	if inv.Safety().DryRun {
		post, meta, err := managedArticleByTransport(ctx, inv, id)
		if err != nil {
			return Result{}, err
		}
		if meta == nil {
			meta = map[string]any{}
		}
		meta["dry_run"] = true
		return postResult("Would "+action+" article", post, meta), nil
	}
	if inv.Mode() == ModeDirect {
		runtime, err := openDirectPostWrite(ctx, inv)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = runtime.Close() }()
		if action == "unpublish" {
			draft, err := runtime.service.WithdrawArticle(ctx, id)
			if err != nil {
				return Result{}, postCLIError(err)
			}
			return articleDraftResult("Unpublished", postdto.FromArticleDraft(draft), nil), nil
		}
		post, err := runtime.service.Delete(ctx, id)
		if err != nil {
			return Result{}, postCLIError(err)
		}
		return postResult("Deleted article", postdto.FromPost(post), map[string]any{"deleted": true}), nil
	}
	if action == "unpublish" {
		var draft postdto.ArticleDraft
		meta, status, err := postAPIRequest(ctx, inv, http.MethodPost, "/api/v1/articles/"+id.String()+"/unpublish", nil, nil, "", true, &draft)
		if err != nil {
			return Result{}, err
		}
		if status != http.StatusOK {
			return Result{}, invalidAPIResponse()
		}
		return articleDraftResult("Unpublished", draft, meta), nil
	}
	var post postdto.Post
	meta, status, err := postAPIRequest(ctx, inv, http.MethodDelete, "/api/v1/articles/"+id.String(), nil, nil, "", true, &post)
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return Result{}, invalidAPIResponse()
	}
	return postResult("Deleted article", post, meta), nil
}
