package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
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
	log            ginhttp.LogFunc
	exporter       *archive.Exporter
	committer      *hostedImportCommitter
	database       *sqlitestore.Database
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
	engine, err := ginhttp.New(ginhttp.Config{
		Assets: host.assets, OwnerAuth: host.auth, SiteSettings: siteService, InstanceID: runtimeState.Instance.ID, InstanceInitializedAt: runtimeState.Instance.InitializedAt,
		Media: mediaStore, Posts: postService, Locations: locationService, Comments: commentService, Search: searchService,
		ShareMetadata: shareMetadata, Archive: host.exporter, ImportInspector: inspector, ImportCommitter: host.committer, Doctor: doctorService,
		SecureCookies: host.secureCookies, TrustedProxies: host.trustedProxies, Log: host.log,
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
	host.mu.Unlock()
	if previous != nil && previous != database {
		return errors.Join(errors.New("live activation replaced an unclosed database"), previous.Close())
	}
	return nil
}
