package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/linkfetch"
	locationprovider "github.com/moment/moment/internal/location"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/server"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

type liveServerHost struct {
	mu             sync.RWMutex
	board          *server.Switchboard
	auth           *auth.Manager
	assets         fs.FS
	trustedProxies []string
	secureCookies  bool
	listenAddress  string
	log            ginhttp.SurfaceLogFunc
	exporter       *archive.Exporter
	committer      *hostedImportCommitter
	database       *sqlitestore.Database
	// accounts follows the live generation the same way database does, so the
	// session janitor sweeps whichever generation is current rather than one
	// captured when the process started.
	accounts *app.AccountService
}

type hostedImportCommitter struct {
	core *archive.Committer
	host *liveServerHost
}

func (committer *hostedImportCommitter) Commit(ctx context.Context, token string, epoch uint64) (archive.CommitResult, error) {
	return committer.core.Commit(ctx, token, epoch, committer.host)
}

func (committer *hostedImportCommitter) CommitArchive(ctx context.Context, source io.Reader, expectedArchiveSHA string, epoch uint64) (archive.CommitResult, error) {
	return committer.core.CommitArchive(ctx, source, expectedArchiveSHA, epoch, committer.host)
}

func (committer *hostedImportCommitter) InspectMergeArchive(ctx context.Context, source io.Reader, epoch uint64) (archive.MergeInspection, error) {
	return committer.core.InspectMergeArchive(ctx, source, epoch, committer.host.ActiveDatabase())
}

func (committer *hostedImportCommitter) InspectReviewArchive(ctx context.Context, source io.Reader, epoch uint64) (archive.MergeInspection, error) {
	return committer.core.InspectReviewArchive(ctx, source, epoch, committer.host.ActiveDatabase())
}

func (committer *hostedImportCommitter) CommitReviewedReplacement(ctx context.Context, token string, epoch uint64, planID string) (archive.CommitResult, error) {
	return committer.core.CommitReviewedReplacement(ctx, token, epoch, planID, committer.host)
}

func (committer *hostedImportCommitter) ReadReviewMedia(ctx context.Context, token string, epoch uint64, planID string, source archive.MergeReviewSource, mediaID string) (media.Asset, error) {
	return committer.core.ReadReviewMedia(ctx, token, epoch, planID, source, mediaID)
}

func (committer *hostedImportCommitter) CommitMerge(ctx context.Context, token string, epoch uint64, selection archive.MergeSelection) (archive.CommitResult, error) {
	return committer.core.CommitMerge(ctx, token, epoch, selection, committer.host)
}

func (committer *hostedImportCommitter) DiscardMerge(ctx context.Context, token string, epoch uint64) error {
	return committer.core.DiscardMerge(ctx, token, epoch)
}

func (committer *hostedImportCommitter) Status(ctx context.Context) (archive.CommitStatus, error) {
	return committer.core.Status(ctx)
}

func (host *liveServerHost) BeginMaintenance(ctx context.Context) error {
	return host.board.BeginMaintenance(ctx)
}

func (host *liveServerHost) CurrentSessionEpoch() (uint64, error) {
	status, err := host.auth.Status()
	return status.SessionEpoch, err
}

func (host *liveServerHost) EndMaintenance() { host.board.EndMaintenance() }

func (host *liveServerHost) ActiveDatabase() *sqlitestore.Database {
	host.mu.RLock()
	defer host.mu.RUnlock()
	return host.database
}

func (host *liveServerHost) CloseActive() error {
	host.mu.Lock()
	database := host.database
	host.database = nil
	host.mu.Unlock()
	if database == nil {
		return nil
	}
	return database.Close()
}

func (host *liveServerHost) Activate(ctx context.Context, runtimeState *runtimefs.Runtime) error {
	if runtimeState == nil {
		return errors.New("resolved runtime is required for live activation")
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return err
	}
	fail := func(err error) error { return errors.Join(err, database.Close()) }
	siteRepository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		return fail(err)
	}
	siteService, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: runtimeState.IsDevelopment()})
	if err != nil {
		return fail(err)
	}
	mediaStore, err := media.NewStore(runtimeState, database, media.Options{})
	if err != nil {
		return fail(err)
	}
	postRepository, err := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		return fail(err)
	}
	postService, err := app.NewPostService(postRepository)
	if err != nil {
		return fail(err)
	}
	locationService, err := app.NewLocationService(locationprovider.NewFromEnvironment())
	if err != nil {
		return fail(err)
	}
	// One resolver, one hardened transport. Link thumbnails and account
	// pictures are both remote images and share the same address filter.
	linkResolver := linkfetch.NewResolver(linkfetch.ResolverOptions{})
	linkService, err := app.NewLinkPreviewService(
		linkResolver,
		media.NewLinkThumbnailAdapter(mediaStore),
		app.LinkPreviewOptions{},
	)
	if err != nil {
		return fail(err)
	}
	commentRepository, err := sqlitestore.NewCommentRepository(database, runtimeState.Locks)
	if err != nil {
		return fail(err)
	}
	commentService, err := app.NewCommentService(commentRepository)
	if err != nil {
		return fail(err)
	}
	searchRepository, err := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		return fail(err)
	}
	searchService, err := app.NewSearchService(searchRepository)
	if err != nil {
		return fail(err)
	}
	shareMetadata, err := app.NewShareMetadataService(siteService, postService)
	if err != nil {
		return fail(err)
	}
	inspector, err := archive.NewInspector(archive.InspectOptions{DataDir: runtimeState.Root, ActiveDatabase: database, ImportLockWait: 250 * time.Millisecond})
	if err != nil {
		return fail(err)
	}
	doctorService, err := doctor.New(doctor.Options{DataDir: runtimeState.Root, ActiveDatabase: database})
	if err != nil {
		return fail(err)
	}
	accountRepository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		return fail(err)
	}
	accountService, err := app.NewAccountService(accountRepository,
		// An account picture is fetched through the same hardened transport a
		// link thumbnail is, and ingested through the same media path an owner
		// upload uses. Remote bytes get no exemption from either.
		app.WithAvatarCapture(linkResolver.Fetcher(), media.NewLinkThumbnailAdapter(mediaStore)),
	)
	if err != nil {
		return fail(err)
	}
	providers, err := host.identityProviders(ctx, siteService)
	if err != nil {
		return fail(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets: host.assets, OwnerAuth: host.auth, Accounts: accountService, OAuthProviders: providers, SiteSettings: siteService, InstanceID: runtimeState.Instance.ID, InstanceInitializedAt: runtimeState.Instance.InitializedAt,
		Media: mediaStore, Posts: postService, Locations: locationService, Links: linkService, Comments: commentService, Search: searchService,
		ShareMetadata: shareMetadata, Archive: host.exporter, ImportInspector: inspector, ImportCommitter: host.committer, Doctor: doctorService,
		SecureCookies: host.secureCookies, TrustedProxies: host.trustedProxies, SurfaceLog: host.log,
		Readiness: func(readyCtx context.Context) error {
			if err := database.Ready(readyCtx); err != nil {
				return err
			}
			return siteService.Ready(readyCtx)
		},
	})
	if err != nil {
		return fail(err)
	}
	if err := database.Ready(ctx); err != nil {
		return fail(err)
	}
	if err := siteService.Ready(ctx); err != nil {
		return fail(err)
	}
	if err := host.board.Swap(engine); err != nil {
		return fail(err)
	}
	host.mu.Lock()
	previous := host.database
	host.database = database
	host.accounts = accountService
	host.mu.Unlock()
	if previous != nil && previous != database {
		return errors.Join(errors.New("live activation replaced an unclosed database"), previous.Close())
	}
	return nil
}

// identityProviders builds the federated sign-in registry for this deployment.
//
// Credentials come from the environment, matching how the location providers
// are configured, and a provider missing either half is simply absent -- the
// sign-in page then draws no button for it. They are not kept in secrets.json,
// which travels with the data directory: an OAuth client belongs to a
// deployment and its redirect URI, not to the content.
func (host *liveServerHost) identityProviders(ctx context.Context, site *app.SiteService) (*oauth.Registry, error) {
	base := host.publicBaseURL(ctx, site)
	if base == "" {
		// Without a canonical address there is no redirect URI to register,
		// and a redirect URI is matched exactly by every provider. Guessing one
		// from the Host header would make the address an attacker-supplied
		// value, so sign-in stays off instead.
		return oauth.NewRegistry(), nil
	}
	callback := func(provider string) string {
		return base + "/api/v1/login/oauth/" + provider + "/callback"
	}
	registered := make([]oauth.Provider, 0, 2)
	googleCredentials := oauth.Credentials{
		ClientID:     os.Getenv("MOMENT_GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("MOMENT_GOOGLE_CLIENT_SECRET"),
	}
	if googleCredentials.Configured() {
		google, err := oauth.NewGoogle(ctx, googleCredentials, callback(oauth.GoogleName))
		if err != nil {
			return nil, err
		}
		if google != nil {
			registered = append(registered, google)
		}
	}
	githubCredentials := oauth.Credentials{
		ClientID:     os.Getenv("MOMENT_GITHUB_CLIENT_ID"),
		ClientSecret: os.Getenv("MOMENT_GITHUB_CLIENT_SECRET"),
	}
	if githubCredentials.Configured() {
		if github := oauth.NewGitHub(githubCredentials, callback(oauth.GitHubName)); github != nil {
			registered = append(registered, github)
		}
	}
	return oauth.NewRegistry(registered...), nil
}

// publicBaseURL reports the address this site answers at, without a trailing
// slash. An empty answer disables federated sign-in.
//
// The configured canonical URL always wins. The fallback keys off the listen
// address being loopback rather than off the environment label, because those
// are different questions and it is the address that decides: a provider
// matches a redirect URI exactly and exempts loopback from its HTTPS rule, so
// a production instance served on 127.0.0.1 has a perfectly valid redirect URI
// and no configured base URL. Deciding by environment would refuse it.
//
// Nothing is guessed from the Host header. A redirect URI assembled from a
// value the caller controls is a redirect URI an attacker chooses.
func (host *liveServerHost) publicBaseURL(ctx context.Context, site *app.SiteService) string {
	if settings, err := site.Get(ctx); err == nil && settings.PublicBaseURL != nil {
		return strings.TrimRight(*settings.PublicBaseURL, "/")
	}
	if host.listenAddress == "" {
		return ""
	}
	address, port, err := net.SplitHostPort(host.listenAddress)
	if err != nil || port == "" {
		return ""
	}
	if !isLoopbackHost(address) {
		return ""
	}
	// The literal 127.0.0.1 matters: "localhost" and "127.0.0.1" are two
	// different registrations under exact matching.
	return "http://127.0.0.1:" + port
}

// isLoopbackHost reports whether a listen host only ever accepts connections
// from this machine.
func isLoopbackHost(address string) bool {
	if address == "" || address == "localhost" {
		return true
	}
	parsed := net.ParseIP(address)
	return parsed != nil && parsed.IsLoopback()
}

// RunSessionRetention removes sessions that stopped meaning anything.
//
// It lives here rather than beside the account service because the service is
// rebuilt on every generation swap: reading the current one per sweep is what
// keeps an import commit from leaving the janitor pointed at a closed database.
//
// Nothing reads an expired session -- the signed cookie's own expiry is checked
// before the row is -- so this is housekeeping. It exists because the table
// otherwise only grew: every sign-in added a row and nothing ever removed one.
func (host *liveServerHost) RunSessionRetention(ctx context.Context) {
	sweep := func() {
		host.mu.RLock()
		accounts := host.accounts
		host.mu.RUnlock()
		if accounts == nil {
			return
		}
		_, _ = accounts.SweepExpiredSessions(ctx)
	}
	sweep()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
