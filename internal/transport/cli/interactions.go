package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/transport/interactiondto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerInteractions(application *Application) {
	for _, kind := range []string{domain.SubjectPost, domain.SubjectComment} {
		for _, verb := range []string{"like", "unlike", "like-status", "reply-context", "mentions"} {
			short := map[string]string{"like": "Like one " + kind, "unlike": "Remove a like from one " + kind, "like-status": "Read one " + kind + " like state", "reply-context": "Read one " + kind + " reply participants", "mentions": "Read one " + kind + " saved mention identities"}[verb]
			spec := LeafSpec{Path: []string{kind, verb}, ArgsUsage: "<id>", Short: short, Long: "Inspect or change one account-scoped content interaction using the shared application service.", Inputs: "Provide a canonical content ULID. Direct account operations require --as; API operations use the credential holder.", Output: "The canonical like state or server-derived reply participants.", Errors: "Invalid input, unavailable content and authorization failures return stable codes.", Example: "moment --as writer " + kind + " " + verb + " 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json", Args: cobra.ExactArgs(1), Capability: CapabilityBoth}
			if verb == "mentions" {
				spec.Long = "Read saved mention bindings after applying current source visibility. A saved account identity survives handle changes; unresolved and external display identities remain unlinked."
				spec.Output = "An array of original handles and nullable current account projections."
			}
			spec.Run = func(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
				return runContentInteraction(ctx, inv, input, kind, verb)
			}
			application.MustRegisterLeaf(spec)
		}
	}
	for _, verb := range []string{"list", "get", "state", "seen"} {
		args := cobra.NoArgs
		usage := ""
		if verb == "get" {
			args = cobra.ExactArgs(1)
			usage = "<notification-id>"
		}
		spec := LeafSpec{Path: []string{"notification", verb}, ArgsUsage: usage, Short: map[string]string{"list": "List account notifications", "get": "Read a notification and its current source", "state": "Read notification progress", "seen": "Acknowledge a notification view"}[verb], Long: "Read account activity without changing human viewing progress. Only seen explicitly acknowledges the supplied view boundary.", Inputs: "Direct mode requires --as; API mode uses the credential holder. List supports opaque older or incremental cursors and all/mentions scopes.", Output: "One JSON envelope containing a page, full current source, or notification progress.", Errors: "Invalid or cross-account cursors, missing events and authentication failures return stable codes.", Example: "moment --as writer notification " + verb + " --data-dir ./moment-data --json", Args: args, Capability: CapabilityBoth}
		if verb == "get" {
			spec.Example = "moment --as writer notification get 01ARZ3NDEKTSV4RRFFQ69G5FAV --data-dir ./moment-data --json"
		}
		spec.Flags = func(flags *pflag.FlagSet) {
			if verb == "list" || verb == "seen" {
				flags.String("scope", "all", "view scope: all or mentions")
			}
			if verb == "list" {
				flags.String("cursor", "", "opaque older-page cursor")
				flags.String("after", "", "opaque incremental checkpoint")
				flags.String("reason", "", "filter: reply, mention, conversation or like")
				flags.Int("limit", 0, "maximum records (1-100)")
			}
			if verb == "seen" {
				flags.String("through", "", "through_cursor returned by the same account and view")
			}
		}
		spec.Run = func(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
			return runNotification(ctx, inv, input, verb)
		}
		application.MustRegisterLeaf(spec)
	}
}

func interactionResult(value any) (Result, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return Result{Data: value, Text: string(data)}, err
}
func runContentInteraction(ctx context.Context, inv Invocation, input CommandInput, kind, verb string) (Result, error) {
	id, err := domain.ParseID(input.Args()[0])
	if err != nil {
		return Result{}, commentCLIError(err)
	}
	ref := domain.ContentRef{Kind: kind, ID: id}
	var result any
	if inv.Mode() == ModeDirect {
		service, cleanup, err := openDirectInteractions(ctx, inv)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		defer cleanup()
		switch verb {
		case "mentions":
			result, err = service.BoundMentions(ctx, ref, app.ViewerFromContext(ctx))
		case "reply-context":
			result, err = service.ReplyContext(ctx, ref)
		case "like-status":
			result, err = service.LikeStatus(ctx, ref, app.ViewerFromContext(ctx))
		default:
			result, err = service.Like(ctx, ref, verb == "like")
		}
		if err != nil {
			return Result{}, commentCLIError(err)
		}
	} else {
		method := http.MethodGet
		resource := "posts"
		if kind == domain.SubjectComment {
			resource = "comments"
		}
		suffix := "like"
		auth := false
		if verb == "reply-context" {
			suffix = verb
			auth = true
		}
		if verb == "like" {
			method = http.MethodPut
			auth = true
		}
		if verb == "unlike" {
			method = http.MethodDelete
			auth = true
		}
		if verb == "mentions" {
			suffix = "mentions"
		}
		if suffix == "like" {
			result = &domain.LikeState{}
		} else if verb == "mentions" {
			result = &[]app.BoundMention{}
		} else {
			result = &app.ReplyContext{}
		}
		_, status, err := commentAPIRequest(ctx, inv, method, "/api/v1/"+resource+"/"+id.String()+"/"+suffix, nil, nil, "", auth, result)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		if status != 200 {
			return Result{}, invalidAPIResponse()
		}
	}
	return interactionResult(result)
}

func runNotification(ctx context.Context, inv Invocation, input CommandInput, verb string) (Result, error) {
	query := url.Values{}
	params := app.NotificationListInput{}
	if verb == "list" || verb == "seen" {
		value, _ := input.String("scope")
		params.Scope = value
		query.Set("scope", value)
	}
	var through string
	if verb == "list" {
		for _, name := range []string{"cursor", "after", "reason"} {
			value, _ := input.String(name)
			if input.Changed(name) && value == "" {
				return Result{}, invalidArgument(name+" cannot be empty", "omit unused flags")
			}
			if value != "" {
				query.Set(name, value)
			}
		}
		params.Cursor = query.Get("cursor")
		params.After = query.Get("after")
		params.Reason = query.Get("reason")
		params.Limit, _ = input.Int("limit")
		if input.Changed("limit") {
			if params.Limit < 1 || params.Limit > 100 {
				return Result{}, invalidArgument("limit must be between 1 and 100", "choose a bounded page size")
			}
			query.Set("limit", strconv.Itoa(params.Limit))
		}
	}
	if verb == "seen" {
		through, _ = input.String("through")
		if through == "" {
			return Result{}, invalidArgument("--through is required", "use the through_cursor from notification list for the selected scope")
		}
	}
	id := ""
	if verb == "get" {
		parsed, err := domain.ParseID(input.Args()[0])
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		id = parsed.String()
	}
	var result any
	if inv.Mode() == ModeDirect {
		if _, err := directContentActor(ctx); err != nil {
			return Result{}, err
		}
		service, cleanup, err := openDirectInteractions(ctx, inv)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		defer cleanup()
		switch verb {
		case "list":
			page, e := service.Notifications(ctx, params)
			err = e
			result = interactiondto.FromPage(page)
		case "get":
			item, e := service.Notification(ctx, id)
			err = e
			result = interactiondto.FromItem(item)
		case "state":
			result, err = service.NotificationState(ctx)
		case "seen":
			result, err = service.SeeNotifications(ctx, params.Scope, through)
		}
		if err != nil {
			return Result{}, commentCLIError(err)
		}
	} else {
		endpoint := "/api/v1/account/notifications"
		method := http.MethodGet
		var payload any
		switch verb {
		case "list":
			result = &interactiondto.Page{}
		case "get":
			endpoint += "/" + id
			result = &interactiondto.Notification{}
		case "state":
			endpoint += "/state"
			result = &app.NotificationState{}
		case "seen":
			endpoint += "/seen"
			method = http.MethodPost
			payload = map[string]string{"scope": params.Scope, "through_cursor": through}
			result = &app.NotificationState{}
			query = nil
		}
		_, status, err := commentAPIRequest(ctx, inv, method, endpoint, query, payload, "", true, result)
		if err != nil {
			return Result{}, commentCLIError(err)
		}
		if status != 200 {
			return Result{}, invalidAPIResponse()
		}
	}
	return interactionResult(result)
}

func openDirectInteractions(ctx context.Context, inv Invocation) (*app.InteractionService, func(), error) {
	target, err := inv.DirectTransport()
	if err != nil {
		return nil, nil, err
	}
	runtime, lease, err := runtimefs.OpenForWriteContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, runtimeError(err)
	}
	db, err := sqlitestore.Open(ctx, runtime, sqlitestore.Options{})
	if err != nil {
		_ = lease.Release()
		return nil, nil, postDatabaseError(err)
	}
	cleanup := func() { _ = db.Close(); _ = lease.Release() }
	fail := func(err error) (*app.InteractionService, func(), error) { cleanup(); return nil, nil, err }
	p, err := sqlitestore.NewPostRepository(db, runtime.Locks)
	if err != nil {
		return fail(err)
	}
	posts, err := app.NewPostService(p)
	if err != nil {
		return fail(err)
	}
	c, err := sqlitestore.NewCommentRepository(db, runtime.Locks)
	if err != nil {
		return fail(err)
	}
	comments, err := app.NewCommentService(c)
	if err != nil {
		return fail(err)
	}
	r, err := sqlitestore.NewInteractionRepository(db, runtime.Locks, runtime.Current.ID)
	if err != nil {
		return fail(err)
	}
	service, err := app.NewInteractionService(r, posts, comments)
	if err != nil {
		return fail(err)
	}
	return service, cleanup, nil
}
