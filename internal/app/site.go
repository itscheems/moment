package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
)

// SiteSettings is the portable, public site identity shared by every adapter.
// Authentication material and deployment-derived request data never belong in
// this value.
type SiteSettings struct {
	ProfileUserID   string  `json:"profile_user_id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Title           string  `json:"title"`
	Bio             string  `json:"bio"`
	Timezone        string  `json:"timezone"`
	PublicBaseURL   *string `json:"public_base_url"`
	AvatarMediaID   *string `json:"avatar_media_id"`
	CoverMediaID    *string `json:"cover_media_id"`
	PinnedPostID    *string `json:"pinned_post_id"`
	CommentsEnabled bool    `json:"comments_enabled"`
	// RegistrationMode decides what happens to an external identity the site
	// has not seen before.
	//
	// It is never serialised here. GET /api/v1/site is public, and this value
	// is site policy rather than public identity -- telling an anonymous reader
	// whether applications are being accepted is the owner's decision to
	// publish, not a field that leaks by being in the same struct. The owner
	// reads it from the account review endpoint, which is already about
	// exactly this.
	RegistrationMode domain.RegistrationMode `json:"-"`
	// UpdatedAtMS stamps the owner account row when a write touches profile
	// fields. It is not part of the public projection.
	UpdatedAtMS int64 `json:"-"`
}

// MentionSuggestion is the bounded public identity projection used by
// composers. ProfileUserID remains stable when the editable username changes.
type MentionSuggestion struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id"`
	ProfileURL    string  `json:"profile_url"`
}

// NullableString distinguishes an omitted PATCH field from an explicit null.
type NullableString struct {
	Value *string
}

// SiteUpdate contains transport-independent partial update intent.
type SiteUpdate struct {
	Username         *string
	DisplayName      *string
	Title            *string
	Bio              *string
	Timezone         *string
	PublicBaseURL    *NullableString
	AvatarMediaID    *NullableString
	CoverMediaID     *NullableString
	CommentsEnabled  *bool
	RegistrationMode *string
}

// Empty reports whether an update carries no fields.
func (update SiteUpdate) Empty() bool {
	return update.Username == nil && update.DisplayName == nil && update.Title == nil && update.Bio == nil && update.Timezone == nil &&
		update.PublicBaseURL == nil && update.AvatarMediaID == nil && update.CoverMediaID == nil && update.CommentsEnabled == nil &&
		update.RegistrationMode == nil
}

// SiteStore is the transaction-bound settings persistence surface.
type SiteStore interface {
	MediaReferenceReconciler
	SiteSettings(context.Context) (SiteSettings, error)
	MediaExists(context.Context, domain.ID) (bool, error)
	EntryExists(context.Context, domain.ID) (bool, error)
	UpdateSiteSettings(context.Context, SiteSettings) error
}

// SiteTransactions supplies atomic settings reads and writes without exposing
// database ownership to application services.
type SiteTransactions interface {
	Read(context.Context, func(SiteStore) error) error
	Write(context.Context, func(SiteStore) error) error
}

// SiteOptions binds validation to persistent instance metadata, not request
// headers. Development instances may use loopback HTTP public URLs.
type SiteOptions struct {
	AllowDevelopmentLocalHTTP bool
	Clock                     func() time.Time
}

// SiteService owns validation and atomic site settings changes.
type SiteService struct {
	transactions SiteTransactions
	allowHTTP    bool
	clock        func() time.Time
}

// NewSiteService constructs the shared settings authority.
func NewSiteService(transactions SiteTransactions, options SiteOptions) (*SiteService, error) {
	if transactions == nil {
		return nil, errors.New("site transactions are required")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &SiteService{
		transactions: transactions,
		allowHTTP:    options.AllowDevelopmentLocalHTTP,
		clock:        options.Clock,
	}, nil
}

// HostTimezone returns an IANA location when the process exposes one and UTC
// otherwise. Go's synthetic "Local" name is deliberately not persisted.
func HostTimezone() string {
	return detectHostTimezone(hostTimezoneSources{
		tz:        os.Getenv("TZ"),
		localName: time.Local.String(),
		readlink:  os.Readlink,
		readFile:  os.ReadFile,
	})
}

type hostTimezoneSources struct {
	tz        string
	localName string
	readlink  func(string) (string, error)
	readFile  func(string) ([]byte, error)
}

func detectHostTimezone(sources hostTimezoneSources) string {
	tz := strings.TrimPrefix(strings.TrimSpace(sources.tz), ":")
	candidates := []string{tz, timezoneNameFromPath(tz), sources.localName}
	if sources.readlink != nil {
		if target, err := sources.readlink("/etc/localtime"); err == nil {
			candidates = append(candidates, timezoneNameFromPath(target))
		}
	}
	if sources.readFile != nil {
		if contents, err := sources.readFile("/etc/timezone"); err == nil {
			candidates = append(candidates, strings.TrimSpace(string(contents)))
		}
	}
	for _, candidate := range candidates {
		if validateTimezone(candidate) == nil {
			return candidate
		}
	}
	return "UTC"
}

func timezoneNameFromPath(value string) string {
	value = filepathSlash(value)
	for _, marker := range []string{"/zoneinfo/", "zoneinfo/"} {
		if index := strings.LastIndex(value, marker); index >= 0 {
			return strings.TrimPrefix(value[index+len(marker):], "/")
		}
	}
	return ""
}

func filepathSlash(value string) string { return strings.ReplaceAll(value, "\\", "/") }

// Get returns the migration-created singleton settings row using a read-only
// transaction. A missing row is corruption; reads never silently recreate it.
func (service *SiteService) Get(ctx context.Context) (SiteSettings, error) {
	if err := contextError(ctx); err != nil {
		return SiteSettings{}, err
	}
	var settings SiteSettings
	err := service.transactions.Read(ctx, func(store SiteStore) error {
		var err error
		settings, err = store.SiteSettings(ctx)
		if err != nil {
			return err
		}
		return service.validateStored(ctx, store, settings)
	})
	if err != nil {
		return SiteSettings{}, normalizeSiteRepositoryError(err)
	}
	return cloneSiteSettings(settings), nil
}

// Ready verifies that the singleton and its managed avatar reference remain
// valid. It is composed with database readiness by the server.
func (service *SiteService) Ready(ctx context.Context) error {
	_, err := service.Get(ctx)
	return err
}

// Pin atomically selects one published Moment for public profile emphasis.
// Assigning the current value is an idempotent success and never changes the
// Moment's chronological fields.
func (service *SiteService) Pin(ctx context.Context, id domain.ID) (SiteSettings, error) {
	if err := contextError(ctx); err != nil {
		return SiteSettings{}, err
	}
	if _, err := domain.ParseID(id.String()); err != nil {
		return SiteSettings{}, err
	}
	var result SiteSettings
	err := service.transactions.Write(ctx, func(store SiteStore) error {
		current, err := store.SiteSettings(ctx)
		if err != nil {
			return err
		}
		if err := service.validateStored(ctx, store, current); err != nil {
			return err
		}
		exists, err := store.EntryExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return postNotFound(id)
		}
		value := id.String()
		current.PinnedPostID = &value
		if err := store.UpdateSiteSettings(ctx, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	if err != nil {
		return SiteSettings{}, normalizeSiteRepositoryError(err)
	}
	return cloneSiteSettings(result), nil
}

// Unpin clears the singleton selection. Repeated clears are safe successes.
func (service *SiteService) Unpin(ctx context.Context, expected ...domain.ID) (SiteSettings, error) {
	if err := contextError(ctx); err != nil {
		return SiteSettings{}, err
	}
	var result SiteSettings
	err := service.transactions.Write(ctx, func(store SiteStore) error {
		current, err := store.SiteSettings(ctx)
		if err != nil {
			return err
		}
		if err := service.validateStored(ctx, store, current); err != nil {
			return err
		}
		if len(expected) > 1 {
			return domain.NewError(domain.CodeInvalidArgument, "at most one pinned post may be selected for removal", nil)
		}
		if len(expected) == 1 && current.PinnedPostID != nil && *current.PinnedPostID != expected[0].String() {
			return postNotFound(expected[0])
		}
		if current.PinnedPostID != nil {
			current.PinnedPostID = nil
			if err := store.UpdateSiteSettings(ctx, current); err != nil {
				return err
			}
		}
		result = current
		return nil
	})
	if err != nil {
		return SiteSettings{}, normalizeSiteRepositoryError(err)
	}
	return cloneSiteSettings(result), nil
}

// Update validates every supplied field before committing one complete row.
// Missing or invalid managed media causes the entire PATCH to roll back.
func (service *SiteService) Update(ctx context.Context, update SiteUpdate) (SiteSettings, error) {
	if err := contextError(ctx); err != nil {
		return SiteSettings{}, err
	}
	if update.Empty() {
		return SiteSettings{}, domain.NewError(domain.CodeInvalidArgument, "at least one site setting is required", nil)
	}
	prepared, err := service.prepare(update)
	if err != nil {
		return SiteSettings{}, err
	}

	var result SiteSettings
	err = service.transactions.Write(ctx, func(store SiteStore) error {
		current, err := store.SiteSettings(ctx)
		if err != nil {
			return err
		}
		if err := service.validateStored(ctx, store, current); err != nil {
			return err
		}
		next := applySiteUpdate(current, prepared)
		// The owner's half of this value lives on the account row now, and
		// that row records when it last changed. Stamping here rather than in
		// the store keeps the clock in the one layer that owns it.
		next.UpdatedAtMS = domain.TimeToMillis(domain.NormalizeTime(service.clock()))
		for _, media := range []struct {
			field string
			id    *string
		}{
			{field: "avatar_media_id", id: next.AvatarMediaID},
			{field: "cover_media_id", id: next.CoverMediaID},
		} {
			if media.id == nil {
				continue
			}
			id, parseErr := domain.ParseID(*media.id)
			if parseErr != nil {
				return domain.NewError(domain.CodeInvalidArgument, media.field+" must be a valid managed media ID", map[string]any{"field": media.field})
			}
			exists, lookupErr := store.MediaExists(ctx, id)
			if lookupErr != nil {
				return lookupErr
			}
			if !exists {
				return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", id), map[string]any{"media_id": id.String()})
			}
		}
		if err := store.UpdateSiteSettings(ctx, next); err != nil {
			return err
		}
		if update.AvatarMediaID != nil || update.CoverMediaID != nil {
			affected, affectedErr := uniqueMediaIDs(current.AvatarMediaID, next.AvatarMediaID, current.CoverMediaID, next.CoverMediaID)
			if affectedErr != nil {
				return domain.NewError(domain.CodeDataIntegrityFailed, "stored profile media ID is invalid", nil)
			}
			if err := store.ReconcileMediaReferences(ctx, affected, domain.NormalizeTime(service.clock())); err != nil {
				return err
			}
		}
		result = next
		return nil
	})
	if err != nil {
		return SiteSettings{}, normalizeSiteRepositoryError(err)
	}
	return cloneSiteSettings(result), nil
}

func (service *SiteService) prepare(update SiteUpdate) (SiteUpdate, error) {
	prepared := update
	// The owner edits the same three columns from two places -- here and
	// PATCH /api/v1/account/profile -- so both go through the domain
	// normalizers rather than restating the rules. A second copy of the rules
	// is a second set of values one route accepts and the other refuses, and
	// the column cannot hold both answers.
	if update.Username != nil {
		value, err := domain.NormalizeUsername(*update.Username)
		if err != nil {
			return SiteUpdate{}, err
		}
		prepared.Username = &value
	}
	if update.DisplayName != nil {
		value, err := domain.NormalizeDisplayName(*update.DisplayName)
		if err != nil {
			return SiteUpdate{}, err
		}
		prepared.DisplayName = &value
	}
	if update.Title != nil && (strings.TrimSpace(*update.Title) == "" || len([]rune(*update.Title)) > 120) {
		return SiteUpdate{}, domain.NewError(domain.CodeInvalidArgument, "title must contain 1 to 120 characters", map[string]any{"field": "title"})
	}
	if update.Bio != nil {
		value, err := domain.NormalizeBio(*update.Bio)
		if err != nil {
			return SiteUpdate{}, err
		}
		prepared.Bio = &value
	}
	if update.RegistrationMode != nil {
		if err := domain.ValidateRegistrationMode(domain.RegistrationMode(*update.RegistrationMode)); err != nil {
			return SiteUpdate{}, err
		}
	}
	if update.Timezone != nil {
		if err := validateTimezone(*update.Timezone); err != nil {
			return SiteUpdate{}, err
		}
	}
	if update.PublicBaseURL != nil && update.PublicBaseURL.Value != nil {
		normalized, err := NormalizePublicBaseURL(*update.PublicBaseURL.Value, service.allowHTTP)
		if err != nil {
			return SiteUpdate{}, err
		}
		prepared.PublicBaseURL = &NullableString{Value: &normalized}
	}
	if update.AvatarMediaID != nil && update.AvatarMediaID.Value != nil {
		value := strings.TrimSpace(*update.AvatarMediaID.Value)
		if _, err := domain.ParseID(value); err != nil {
			return SiteUpdate{}, domain.NewError(domain.CodeInvalidArgument, "avatar_media_id must be a valid managed media ID", map[string]any{"field": "avatar_media_id"})
		}
		prepared.AvatarMediaID = &NullableString{Value: &value}
	}
	if update.CoverMediaID != nil && update.CoverMediaID.Value != nil {
		value := strings.TrimSpace(*update.CoverMediaID.Value)
		if _, err := domain.ParseID(value); err != nil {
			return SiteUpdate{}, domain.NewError(domain.CodeInvalidArgument, "cover_media_id must be a valid managed media ID", map[string]any{"field": "cover_media_id"})
		}
		prepared.CoverMediaID = &NullableString{Value: &value}
	}
	return prepared, nil
}

func (service *SiteService) validateStored(ctx context.Context, store SiteStore, settings SiteSettings) error {
	// Shape only, deliberately: the reserved list and the character rules are
	// write-time constraints on creating something broken. Re-checking a
	// stored row against a list that grew, or against a rule tightened after
	// the row was written, would turn an owner's own settings page into the
	// screen that refuses to load and refuses to let them fix it.
	if !validProfileUserID(settings.ProfileUserID) || domain.ValidateStoredUsername(settings.Username) != nil || strings.TrimSpace(settings.DisplayName) != settings.DisplayName || settings.DisplayName == "" || len([]rune(settings.DisplayName)) > domain.MaxDisplayNameLength ||
		strings.TrimSpace(settings.Title) == "" || len([]rune(settings.Title)) > 120 || len([]rune(settings.Bio)) > domain.MaxBioLength {
		return domain.NewError(domain.CodeDataIntegrityFailed, "stored site identity text is invalid", nil)
	}
	if err := validateTimezone(settings.Timezone); err != nil {
		return domain.NewError(domain.CodeDataIntegrityFailed, "stored site timezone is invalid", map[string]any{"field": "timezone"})
	}
	if settings.PublicBaseURL != nil {
		normalized, err := NormalizePublicBaseURL(*settings.PublicBaseURL, service.allowHTTP)
		if err != nil || normalized != *settings.PublicBaseURL {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored public base URL is invalid", map[string]any{"field": "public_base_url"})
		}
	}
	if settings.AvatarMediaID != nil {
		id, err := domain.ParseID(*settings.AvatarMediaID)
		if err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored avatar media ID is invalid", map[string]any{"field": "avatar_media_id"})
		}
		exists, err := store.MediaExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored avatar media reference is missing", map[string]any{"media_id": id.String()})
		}
	}
	if settings.CoverMediaID != nil {
		id, err := domain.ParseID(*settings.CoverMediaID)
		if err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored cover media ID is invalid", map[string]any{"field": "cover_media_id"})
		}
		exists, err := store.MediaExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored cover media reference is missing", map[string]any{"media_id": id.String()})
		}
	}
	if settings.PinnedPostID != nil {
		id, err := domain.ParseID(*settings.PinnedPostID)
		if err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored pinned post ID is invalid", map[string]any{"field": "pinned_post_id"})
		}
		exists, err := store.EntryExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored pinned post reference is missing", map[string]any{"post_id": id.String()})
		}
	}
	return nil
}

func applySiteUpdate(current SiteSettings, update SiteUpdate) SiteSettings {
	next := cloneSiteSettings(current)
	if update.Username != nil {
		next.Username = *update.Username
	}
	if update.DisplayName != nil {
		next.DisplayName = *update.DisplayName
	}
	if update.Title != nil {
		next.Title = *update.Title
	}
	if update.Bio != nil {
		next.Bio = *update.Bio
	}
	if update.Timezone != nil {
		next.Timezone = *update.Timezone
	}
	if update.PublicBaseURL != nil {
		next.PublicBaseURL = cloneString(update.PublicBaseURL.Value)
	}
	if update.AvatarMediaID != nil {
		next.AvatarMediaID = cloneString(update.AvatarMediaID.Value)
	}
	if update.CoverMediaID != nil {
		next.CoverMediaID = cloneString(update.CoverMediaID.Value)
	}
	if update.CommentsEnabled != nil {
		next.CommentsEnabled = *update.CommentsEnabled
	}
	if update.RegistrationMode != nil {
		next.RegistrationMode = domain.RegistrationMode(*update.RegistrationMode)
	}
	return next
}

func validProfileUserID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'f') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validateTimezone(value string) error {
	if value == "" || value == "Local" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
		return domain.NewError(domain.CodeInvalidArgument, "timezone must be a valid IANA name", map[string]any{"field": "timezone"})
	}
	if _, err := time.LoadLocation(value); err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "timezone must be a valid IANA name", map[string]any{"field": "timezone"})
	}
	return nil
}

// NormalizePublicBaseURL is the one lossless URL contract shared by settings
// writes, stored-authority checks, and portable archive validation.
func NormalizePublicBaseURL(value string, allowDevelopmentLocalHTTP bool) (string, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return "", invalidPublicBaseURL()
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", invalidPublicBaseURL()
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", invalidPublicBaseURL()
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" || strings.HasSuffix(parsed.Host, ":") || !validURLHost(hostname) {
		return "", invalidPublicBaseURL()
	}
	if parsed.Scheme == "http" && (!allowDevelopmentLocalHTTP || !isLocalHost(hostname)) {
		return "", domain.NewError(domain.CodeInvalidArgument, "public_base_url must use HTTPS outside local development", map[string]any{"field": "public_base_url"})
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", invalidPublicBaseURL()
		}
	}
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = ""
	} else {
		parsed.Path = strings.TrimSuffix(path.Clean("/"+parsed.Path), "/")
	}
	return parsed.String(), nil
}

func invalidPublicBaseURL() error {
	return domain.NewError(domain.CodeInvalidArgument, "public_base_url must be an absolute HTTP(S) base without credentials, query, or fragment", map[string]any{"field": "public_base_url"})
}

func isLocalHost(hostname string) bool {
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func validURLHost(hostname string) bool {
	if net.ParseIP(hostname) != nil || hostname == "localhost" {
		return true
	}
	if len(hostname) > 253 || strings.HasPrefix(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func cloneSiteSettings(settings SiteSettings) SiteSettings {
	settings.PublicBaseURL = cloneString(settings.PublicBaseURL)
	settings.AvatarMediaID = cloneString(settings.AvatarMediaID)
	settings.CoverMediaID = cloneString(settings.CoverMediaID)
	settings.PinnedPostID = cloneString(settings.PinnedPostID)
	return settings
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeSiteRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return domain.NewError(domain.CodeInternal, "site settings storage operation failed", nil)
}
