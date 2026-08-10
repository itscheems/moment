package cli

import (
	"context"
	"fmt"

	"github.com/moment/moment/internal/buildinfo"
)

var momentGroups = []GroupSpec{
	{Name: "owner", Short: "Manage the single site owner", Long: "Inspect and manage the single Moment owner."},
	{Name: "post", Short: "Create and manage Moments", Long: "Create, inspect, update, and delete Moment posts."},
	{Name: "comment", Short: "Inspect and manage comments", Long: "Inspect comments and perform owner comment actions."},
	{Name: "media", Short: "Inspect and maintain managed media", Long: "Add, inspect, verify, and maintain managed Moment media."},
	{Name: "search", Short: "Search and rebuild the memory index", Long: "Query Moment history and maintain its derived search index."},
	{Name: "backup", Short: "Export, inspect, and restore portable data", Long: "Operate the versioned Moment portability protocol."},
	{Name: "site", Short: "Inspect and update site identity", Long: "Inspect and update portable public site settings."},
	{Name: "dev", Short: "Build deterministic development scenarios", Long: "Reset and seed explicitly marked development instances."},
}

// NewMoment composes the public command groups and build metadata command.
// Feature packages add behavior through RegisterLeaf in later composition work.
func NewMoment(info buildinfo.Info) *Application {
	application := New(Config{
		BinaryName: "moment",
		Short:      "A portable personal timeline",
		Long:       "Moment captures a personal timeline now and keeps it portable and searchable later.",
	})
	for _, group := range momentGroups {
		application.MustRegisterGroup(group)
	}
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"version"},
		Short:      "Show build identity",
		Long:       "Show the non-sensitive version, commit, and build date linked into this binary.",
		Inputs:     "No positional input. --json selects the stable machine envelope.",
		Output:     "Build identity in human text or as data in the JSON envelope.",
		Errors:     "Output failures return INTERNAL with exit code 10.",
		Example:    "moment version --json",
		Capability: CapabilityBoth,
		Run: func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
			return Result{
				Data: info,
				Text: fmt.Sprintf("moment %s (%s, %s)", info.Version, info.Commit, info.BuildDate),
			}, nil
		},
	})
	registerServe(application)
	registerOwner(application)
	registerSite(application)
	registerMedia(application)
	registerPosts(application)
	registerComments(application)
	return application
}
