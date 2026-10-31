package archive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// MergeMediaOrigin identifies which immutable generation owns an original.
type MergeMediaOrigin string

// Merge media origins identify which immutable source supplies original bytes.
const (
	MergeMediaLocal    MergeMediaOrigin = "local"
	MergeMediaIncoming MergeMediaOrigin = "incoming"
)

// MergeMediaSource binds one resolved media row to its source original.
type MergeMediaSource struct {
	Origin MergeMediaOrigin
	Media  sqlitestore.ArchiveMedia
}

// ResolvedMerge is the complete portable graph that can be rebuilt into a new
// generation without consulting selection policy again.
type ResolvedMerge struct {
	Authority    PortableAuthority
	MediaSources map[string]MergeMediaSource
	EntryRemap   map[string]string
	CommentRemap map[string]string
}

// MergeResolveOptions supplies deterministic ID seams for keep-both and
// same-ID/different-byte media collisions.
type MergeResolveOptions struct {
	Clock   func() time.Time
	Entropy io.Reader
}

// ResolvePortableMerge validates an explicit selection and produces one
// referentially complete authority graph. Local-only content is never removed.
func ResolvePortableMerge(plan MergePlan, selection MergeSelection, local, incoming PortableAuthority, options MergeResolveOptions) (ResolvedMerge, error) {
	if err := ValidateMergePlan(plan); err != nil {
		return ResolvedMerge{}, err
	}
	if selection.PlanID != plan.PlanID || selection.ArchiveSHA256 != plan.ArchiveSHA256 || selection.BaseGeneration != plan.BaseGeneration || selection.BaseAuthoritySHA256 != plan.BaseAuthoritySHA256 {
		return ResolvedMerge{}, domain.NewError(domain.CodeImportMergePlanStale, "merge selection is not bound to this plan", nil)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Entropy == nil {
		options.Entropy = rand.Reader
	}
	itemChoices, err := validateMergeItemChoices(plan, selection)
	if err != nil {
		return ResolvedMerge{}, err
	}
	siteChoices, err := validateMergeSiteChoices(plan, selection)
	if err != nil {
		return ResolvedMerge{}, err
	}
	usedIDs := map[string]struct{}{}
	for _, values := range [][]string{authorityEntryIDs(local), authorityCommentIDs(local), authorityMediaIDs(local), authorityEntryIDs(incoming), authorityCommentIDs(incoming), authorityMediaIDs(incoming)} {
		for _, id := range values {
			usedIDs[id] = struct{}{}
		}
	}
	for _, rows := range [][]sqlitestore.ArchiveInteraction{local.Interactions, incoming.Interactions} {
		for _, row := range rows {
			if row.Notification != nil {
				usedIDs[row.Notification.ID.String()] = struct{}{}
			}
		}
	}
	newID := func() (string, error) {
		for attempt := 0; attempt < 32; attempt++ {
			value, err := domain.NewID(options.Clock().UTC(), options.Entropy)
			if err != nil {
				return "", err
			}
			id := value.String()
			if _, exists := usedIDs[id]; !exists {
				usedIDs[id] = struct{}{}
				return id, nil
			}
		}
		return "", errors.New("could not allocate a unique merge identifier")
	}

	result := ResolvedMerge{
		Authority: PortableAuthority{Site: local.Site}, MediaSources: map[string]MergeMediaSource{},
		EntryRemap: map[string]string{}, CommentRemap: map[string]string{},
	}
	entries := indexEntries(local.Entries)
	for _, value := range incoming.Entries {
		choice := itemChoices[mergeChoiceKey(MergeEntry, value.ID)]
		switch choice {
		case MergeSkip, MergeKeepLocal:
			if _, exists := entries[value.ID]; exists {
				result.EntryRemap[value.ID] = value.ID
			}
		case MergeAdd, MergeUseIncoming:
			result.EntryRemap[value.ID] = value.ID
		case MergeKeepBoth:
			id, err := newID()
			if err != nil {
				return ResolvedMerge{}, err
			}
			result.EntryRemap[value.ID] = id
		default:
			return ResolvedMerge{}, invalidMergeSelection("entry", value.ID, choice)
		}
	}
	for _, value := range incoming.Entries {
		choice := itemChoices[mergeChoiceKey(MergeEntry, value.ID)]
		if choice == MergeSkip || choice == MergeKeepLocal {
			continue
		}
		resolvedID, ok := result.EntryRemap[value.ID]
		if !ok {
			return ResolvedMerge{}, domain.NewError(domain.CodeInvalidArgument, "selected post has no resolved identity", map[string]any{"post_id": value.ID})
		}
		resolved := cloneArchiveEntry(value)
		resolved.ID = resolvedID
		if value.QuotedEntryID != nil {
			quotedID, retained := result.EntryRemap[*value.QuotedEntryID]
			if !retained {
				return ResolvedMerge{}, domain.NewError(domain.CodeInvalidArgument, "selected quote depends on a skipped post", map[string]any{"post_id": value.ID, "quoted_post_id": *value.QuotedEntryID})
			}
			resolved.QuotedEntryID = &quotedID
		}
		entries[resolvedID] = resolved
	}
	result.Authority.Entries = mapEntries(entries)

	comments := indexComments(local.Comments)
	// First allocate all comment identities so replies can target a parent that
	// appears later in archive order. Only identities this merge can reissue are
	// remapped: entry, comment, and media IDs. AuthorUserID names an account,
	// which this merge neither owns nor reissues, so it travels verbatim.
	for _, value := range incoming.Comments {
		choice := itemChoices[mergeChoiceKey(MergeComment, value.ID)]
		switch choice {
		case MergeSkip, MergeKeepLocal:
			if _, exists := comments[value.ID]; exists {
				result.CommentRemap[value.ID] = value.ID
			}
		case MergeAdd, MergeUseIncoming:
			result.CommentRemap[value.ID] = value.ID
		case MergeKeepBoth:
			id, err := newID()
			if err != nil {
				return ResolvedMerge{}, err
			}
			result.CommentRemap[value.ID] = id
		default:
			return ResolvedMerge{}, invalidMergeSelection("comment", value.ID, choice)
		}
	}
	for _, value := range incoming.Comments {
		choice := itemChoices[mergeChoiceKey(MergeComment, value.ID)]
		if choice == MergeSkip || choice == MergeKeepLocal {
			continue
		}
		resolvedComment := cloneArchiveComment(value)
		resolvedComment.ID = result.CommentRemap[value.ID]
		entryID, retained := result.EntryRemap[value.EntryID]
		if !retained {
			if _, localExists := entries[value.EntryID]; localExists {
				entryID, retained = value.EntryID, true
			}
		}
		if !retained {
			return ResolvedMerge{}, domain.NewError(domain.CodeInvalidArgument, "selected comment depends on a skipped post", map[string]any{"comment_id": value.ID, "entry_id": value.EntryID})
		}
		resolvedComment.EntryID = entryID
		if value.ParentID != nil {
			parentID, exists := result.CommentRemap[*value.ParentID]
			if !exists {
				if _, localExists := comments[*value.ParentID]; localExists {
					parentID, exists = *value.ParentID, true
				}
			}
			if !exists {
				return ResolvedMerge{}, domain.NewError(domain.CodeInvalidArgument, "selected reply depends on a skipped parent comment", map[string]any{"comment_id": value.ID, "parent_id": *value.ParentID})
			}
			resolvedComment.ParentID = &parentID
		}
		// The reply target names a comment, and MergeKeepBoth reissues comment
		// identities, so the annotation follows the same remap parent_id gets or
		// it lands on a stranger. The allocation loop above has already assigned
		// every identity, so answering a sibling remapped in this same pass
		// resolves regardless of the order the two are visited in. Whether the
		// result is one the schema will accept is settled afterwards, against
		// the finished graph.
		resolvedComment.ReplyToCommentID = nil
		if value.ReplyToCommentID != nil {
			targetID := *value.ReplyToCommentID
			if remapped, exists := result.CommentRemap[targetID]; exists {
				targetID = remapped
			}
			resolvedComment.ReplyToCommentID = &targetID
		}
		comments[resolvedComment.ID] = resolvedComment
	}
	// Comment identities are now allocated; redirect only selected incoming quotes.
	for _, value := range incoming.Entries {
		if value.QuotedCommentID == nil {
			continue
		}
		choice := itemChoices[mergeChoiceKey(MergeEntry, value.ID)]
		if choice == MergeSkip || choice == MergeKeepLocal {
			continue
		}
		target, exists := result.CommentRemap[*value.QuotedCommentID]
		if !exists {
			return ResolvedMerge{}, domain.NewError(domain.CodeInvalidArgument, "selected quote depends on a skipped comment", map[string]any{"post_id": value.ID, "quoted_comment_id": *value.QuotedCommentID})
		}
		id := result.EntryRemap[value.ID]
		entry := entries[id]
		entry.QuotedCommentID = &target
		entries[id] = entry
	}
	result.Authority.Entries = mapEntries(entries)
	clearStrandedReplyTargets(comments)
	result.Authority.Comments = topologicalComments(comments)

	localMediaByID, localMediaBySHA := indexMedia(local.Media)
	incomingMediaByID, _ := indexMedia(incoming.Media)
	mediaRemap := map[string]string{}
	for _, value := range incoming.Media {
		if matched, exists := localMediaBySHA[value.SHA256]; exists {
			mediaRemap[value.ID] = matched.ID
			continue
		}
		id := value.ID
		if _, collision := localMediaByID[id]; collision {
			id, err = newID()
			if err != nil {
				return ResolvedMerge{}, err
			}
		}
		mediaRemap[value.ID] = id
	}
	for index := range result.Authority.Entries {
		if source, ok := selectedIncomingEntry(result.Authority.Entries[index].ID, incoming.Entries, result.EntryRemap, itemChoices); ok {
			remapUsages(result.Authority.Entries[index].Media, mediaRemap)
			_ = source
		}
	}
	for index := range result.Authority.Comments {
		if selectedIncomingComment(result.Authority.Comments[index].ID, result.CommentRemap, incoming.Comments, itemChoices) {
			remapUsages(result.Authority.Comments[index].Media, mediaRemap)
			if snapshot := result.Authority.Comments[index].AuthorSnapshot; snapshot != nil {
				snapshot.AvatarMediaID = remappedPointer(snapshot.AvatarMediaID, mediaRemap)
			}
		}
	}
	applySiteSelection(&result.Authority.Site, incoming.Site, siteChoices, result.EntryRemap, mediaRemap)
	// Instance identity and canonical public URL always remain local.
	//
	// Keeping profile_user_id local used to be the entire account story, and it was
	// enough only while there was exactly one account and it was implied by the
	// site row. Now that accounts are real rows, this line would silently drop
	// every account the backup carries -- including the ones the comments it is
	// about to restore were written by. The accounts are resolved explicitly
	// below; this line survives because which account owns the instance is
	// still a property of the instance, not of the backup.
	result.Authority.Site.ProfileUserID = local.Site.ProfileUserID
	result.Authority.Site.PublicBaseURL = local.Site.PublicBaseURL

	result.Authority.Users, result.Authority.Identities, err = resolveMergeAccounts(local, incoming, result.Authority.Site, mediaRemap)
	if err != nil {
		return ResolvedMerge{}, err
	}

	referenced := referencedMediaIDs(result.Authority)
	retainGrantedMedia(referenced, result.Authority.Users, local.Media, incoming.Media, mediaRemap)
	for id := range referenced {
		if value, exists := localMediaByID[id]; exists {
			result.Authority.Media = append(result.Authority.Media, value)
			result.MediaSources[id] = MergeMediaSource{Origin: MergeMediaLocal, Media: value}
			continue
		}
		var source sqlitestore.ArchiveMedia
		found := false
		for incomingID, outputID := range mediaRemap {
			if outputID == id {
				source, found = incomingMediaByID[incomingID]
				break
			}
		}
		if !found {
			return ResolvedMerge{}, domain.NewError(domain.CodeDataIntegrityFailed, "resolved merge references unknown media", map[string]any{"media_id": id})
		}
		output := source
		output.ID = id
		result.Authority.Media = append(result.Authority.Media, output)
		result.MediaSources[id] = MergeMediaSource{Origin: MergeMediaIncoming, Media: source}
	}
	mergeMediaPermissions(&result.Authority, local, incoming, mediaRemap)
	contentRefs := referencedMediaIDs(result.Authority)
	for i := range result.Authority.Media {
		value := &result.Authority.Media[i]
		if _, exists := contentRefs[value.ID]; exists {
			value.UnreferencedSinceMS = nil
		} else if value.UnreferencedSinceMS == nil {
			at := options.Clock().UnixMilli()
			value.UnreferencedSinceMS = &at
		}
	}

	result.Authority.Media = sortedMedia(result.Authority.Media)
	result.Authority.Interactions, err = mergeInteractions(local, incoming, result, itemChoices, newID)
	if err != nil {
		return ResolvedMerge{}, err
	}
	return result, nil
}

// handleClaimer enforces one account per handle across both sides of a merge.
//
// It is a type rather than a closure because the same rule has to answer twice
// at different moments: once while the plan is being built, so a collision
// reaches the reviewer before they work through it, and once while the merged
// generation is being resolved, which is the last gate before anything is
// written. Two transcriptions of the rule would be two answers.
type handleClaimer map[string]string

func (claimed handleClaimer) claim(userID, username string) error {
	handle := strings.ToLower(username)
	if holder, taken := claimed[handle]; taken && holder != userID {
		return domain.NewError(domain.CodeUsernameTaken, "the backup and this site both have an account using one username", map[string]any{
			"username": username, "local_user_id": holder, "incoming_user_id": userID,
		})
	}
	claimed[handle] = userID
	return nil
}

func mergeUsernameCollision(local, incoming PortableAuthority) error {
	handles := handleClaimer{}
	known := make(map[string]struct{}, len(local.Users))
	for _, value := range sortedUsers(local.Users) {
		if err := handles.claim(value.ID, value.Username); err != nil {
			return err
		}
		known[value.ID] = struct{}{}
	}
	for _, value := range sortedUsers(incoming.Users) {
		// A backup copy of an account this instance already has is discarded
		// whole, handle included, so it can never collide with anything.
		if _, exists := known[value.ID]; exists {
			continue
		}
		if err := handles.claim(value.ID, value.Username); err != nil {
			return err
		}
	}
	return nil
}

func resolveMergeAccounts(local, incoming PortableAuthority, site sqlitestore.ArchiveSite, mediaRemap map[string]string) ([]sqlitestore.ArchiveUser, []sqlitestore.ArchiveIdentity, error) {
	if len(local.Users) == 0 || len(incoming.Users) == 0 {
		return nil, nil, errors.New("portable merge requires account records")
	}
	accounts := map[string]sqlitestore.ArchiveUser{}
	handles := handleClaimer{}
	claim := func(value sqlitestore.ArchiveUser) error {
		if err := handles.claim(value.ID, value.Username); err != nil {
			return err
		}
		accounts[value.ID] = value
		return nil
	}
	for _, value := range sortedUsers(local.Users) {
		if err := claim(value); err != nil {
			return nil, nil, err
		}
	}
	for _, value := range sortedUsers(incoming.Users) {
		if _, exists := accounts[value.ID]; exists {
			continue
		}
		added := cloneArchiveUser(value)
		added.AvatarMediaID = remappedPointer(added.AvatarMediaID, mediaRemap)
		added.CoverMediaID = remappedPointer(added.CoverMediaID, mediaRemap)

		if err := claim(added); err != nil {
			return nil, nil, err
		}
	}
	_, exists := accounts[site.ProfileUserID]
	if !exists {
		return nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "merged accounts do not contain the display account", map[string]any{"profile_user_id": site.ProfileUserID})
	}

	users := make([]sqlitestore.ArchiveUser, 0, len(accounts))
	for _, value := range accounts {
		users = append(users, value)
	}

	credentials := map[string]sqlitestore.ArchiveIdentity{}
	for _, value := range sortedIdentities(local.Identities) {
		credentials[mergeIdentityID(value.Provider, value.ProviderUserID)] = value
	}
	for _, value := range sortedIdentities(incoming.Identities) {
		key := mergeIdentityID(value.Provider, value.ProviderUserID)
		if _, exists := credentials[key]; exists {
			continue
		}
		if _, known := accounts[value.UserID]; !known {
			return nil, nil, domain.NewError(domain.CodeDataIntegrityFailed, "a backup credential names an account the merge did not keep", map[string]any{
				"provider": value.Provider, "user_id": value.UserID,
			})
		}
		credentials[key] = cloneArchiveIdentity(value)
	}
	identities := make([]sqlitestore.ArchiveIdentity, 0, len(credentials))
	for _, value := range credentials {
		identities = append(identities, value)
	}
	return sortedUsers(users), sortedIdentities(identities), nil
}

// clearStrandedReplyTargets drops every reply annotation the resolved graph
// cannot carry, restating the rule migration 0021 puts in the schema: a
// non-null reply_to_comment_id must name its own root or a sibling under that
// root, in the same post.
//
// The two ends of that rule are chosen independently. A selection may reissue a
// root under MergeKeepBoth while keeping one of its replies local, or move a
// reply under a different root with MergeUseIncoming; each item is valid on its
// own, and validateMergeItemChoices has no cross-item rule that would forbid
// the pair. Resolving the annotation and the parent separately therefore lets
// an answer end up beside one conversation while pointing into another, which
// the insert and update guards abort on — untyped, deep inside Commit, after
// the reviewer has completed the entire plan and selection flow.
//
// Clearing is the resolution rather than a typed failure. This column is an
// annotation and not structure: nothing traverses it, ON DELETE SET NULL
// already defines losing the target as ordinary, and resolveCommentReplyTargets
// drops the annotation rather than fail a read for the same reason. A parent
// that disappears fails the merge because the reply would have nowhere to live;
// a navigation hint that no longer navigates anywhere costs a prefix. Failing
// instead would reject a selection the plan itself offered, so it would only be
// honest once the plan modelled the dependency and every adapter could stop the
// reviewer from choosing it.
//
// The whole graph is swept, not only the incoming half, because a local reply
// can be stranded the same way: MergeUseIncoming can move its target out from
// under their shared root, and the importer checks the row it writes, not where
// the row came from.
func clearStrandedReplyTargets(comments map[string]sqlitestore.ArchiveComment) {
	for id, value := range comments {
		if value.ReplyToCommentID == nil || replyTargetSharesConversation(value, comments) {
			continue
		}
		value.ReplyToCommentID = nil
		comments[id] = value
	}
}

func replyTargetSharesConversation(value sqlitestore.ArchiveComment, comments map[string]sqlitestore.ArchiveComment) bool {
	if value.ParentID == nil || *value.ReplyToCommentID == value.ID {
		return false
	}
	target, exists := comments[*value.ReplyToCommentID]
	if !exists || target.EntryID != value.EntryID {
		return false
	}
	return target.ID == *value.ParentID || (target.ParentID != nil && *target.ParentID == *value.ParentID)
}

func validateMergeItemChoices(plan MergePlan, selection MergeSelection) (map[string]MergeAction, error) {
	expected := map[string]MergeItem{}
	for _, item := range plan.Items {
		if item.Selectable {
			expected[mergeChoiceKey(item.Kind, item.ID)] = item
		}
	}
	choices := map[string]MergeAction{}
	for _, choice := range selection.Items {
		key := mergeChoiceKey(choice.Kind, choice.ID)
		item, exists := expected[key]
		if !exists || choices[key] != "" {
			return nil, invalidMergeSelection(string(choice.Kind), choice.ID, choice.Action)
		}
		allowed := false
		switch item.Classification {
		case MergeUnchanged:
			allowed = choice.Action == MergeSkip
		case MergeIncomingOnly:
			allowed = choice.Action == MergeAdd || choice.Action == MergeSkip
		case MergeConflict:
			allowed = choice.Action == MergeKeepLocal || choice.Action == MergeUseIncoming || choice.Action == MergeKeepBoth
		}
		if !allowed {
			return nil, invalidMergeSelection(string(choice.Kind), choice.ID, choice.Action)
		}
		choices[key] = choice.Action
	}
	if len(choices) != len(expected) {
		return nil, domain.NewError(domain.CodeInvalidArgument, "merge selection must decide every selectable item exactly once", nil)
	}
	return choices, nil
}

func validateMergeSiteChoices(plan MergePlan, selection MergeSelection) (map[string]MergeAction, error) {
	expected := map[string]struct{}{}
	for _, field := range plan.SiteFields {
		expected[field.Field] = struct{}{}
	}
	choices := map[string]MergeAction{}
	for _, choice := range selection.SiteFields {
		_, exists := expected[choice.Field]
		if !exists || choices[choice.Field] != "" || (choice.Action != MergeKeepLocal && choice.Action != MergeUseIncoming) {
			return nil, invalidMergeSelection("site", choice.Field, choice.Action)
		}
		choices[choice.Field] = choice.Action
	}
	if len(choices) != len(expected) {
		return nil, domain.NewError(domain.CodeInvalidArgument, "merge selection must decide every profile field exactly once", nil)
	}
	return choices, nil
}

func invalidMergeSelection(kind, id string, action MergeAction) error {
	return domain.NewError(domain.CodeInvalidArgument, "merge selection contains an invalid action", map[string]any{"kind": kind, "id": id, "action": action})
}

func mergeChoiceKey(kind MergeEntityKind, id string) string { return string(kind) + "\x00" + id }

func authorityEntryIDs(value PortableAuthority) []string {
	var result []string
	for _, item := range value.Entries {
		result = append(result, item.ID)
	}
	return result
}
func authorityCommentIDs(value PortableAuthority) []string {
	var result []string
	for _, item := range value.Comments {
		result = append(result, item.ID)
	}
	return result
}
func authorityMediaIDs(value PortableAuthority) []string {
	var result []string
	for _, item := range value.Media {
		result = append(result, item.ID)
	}
	return result
}

func mapEntries(values map[string]sqlitestore.ArchiveEntry) []sqlitestore.ArchiveEntry {
	result := make([]sqlitestore.ArchiveEntry, 0, len(values))
	for _, value := range values {
		result = append(result, cloneArchiveEntry(value))
	}
	return sortedEntries(result)
}

func topologicalComments(values map[string]sqlitestore.ArchiveComment) []sqlitestore.ArchiveComment {
	remaining := make(map[string]sqlitestore.ArchiveComment, len(values))
	for id, value := range values {
		remaining[id] = cloneArchiveComment(value)
	}
	result := make([]sqlitestore.ArchiveComment, 0, len(values))
	added := map[string]struct{}{}
	for len(remaining) > 0 {
		ids := make([]string, 0, len(remaining))
		for id := range remaining {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		progress := false
		for _, id := range ids {
			value := remaining[id]
			if value.ParentID != nil {
				if _, ok := added[*value.ParentID]; !ok {
					continue
				}
			}
			result = append(result, value)
			added[id] = struct{}{}
			delete(remaining, id)
			progress = true
		}
		if !progress {
			break
		}
	}
	return result
}

func selectedIncomingEntry(outputID string, incoming []sqlitestore.ArchiveEntry, remap map[string]string, choices map[string]MergeAction) (sqlitestore.ArchiveEntry, bool) {
	for _, value := range incoming {
		action := choices[mergeChoiceKey(MergeEntry, value.ID)]
		if remap[value.ID] == outputID && (action == MergeAdd || action == MergeUseIncoming || action == MergeKeepBoth) {
			return value, true
		}
	}
	return sqlitestore.ArchiveEntry{}, false
}
func selectedIncomingComment(outputID string, remap map[string]string, incoming []sqlitestore.ArchiveComment, choices map[string]MergeAction) bool {
	for _, value := range incoming {
		action := choices[mergeChoiceKey(MergeComment, value.ID)]
		if remap[value.ID] == outputID && (action == MergeAdd || action == MergeUseIncoming || action == MergeKeepBoth) {
			return true
		}
	}
	return false
}
func remapUsages(values []sqlitestore.ArchiveUsage, remap map[string]string) {
	for index := range values {
		if id := remap[values[index].MediaID]; id != "" {
			values[index].MediaID = id
		}
	}
}

func applySiteSelection(site *sqlitestore.ArchiveSite, incoming sqlitestore.ArchiveSite, choices map[string]MergeAction, entryRemap, mediaRemap map[string]string) {
	if choices["username"] == MergeUseIncoming {
		site.Username = incoming.Username
	}
	if choices["display_name"] == MergeUseIncoming {
		site.DisplayName = incoming.DisplayName
	}
	if choices["title"] == MergeUseIncoming {
		site.Title = incoming.Title
	}
	if choices["bio"] == MergeUseIncoming {
		site.Bio = incoming.Bio
	}
	if choices["timezone"] == MergeUseIncoming {
		site.Timezone = incoming.Timezone
	}
	if choices["comments_enabled"] == MergeUseIncoming {
		site.CommentsEnabled = incoming.CommentsEnabled
	}
	if choices["avatar_media_id"] == MergeUseIncoming {
		site.AvatarMediaID = remappedPointer(incoming.AvatarMediaID, mediaRemap)
	}
	if choices["cover_media_id"] == MergeUseIncoming {
		site.CoverMediaID = remappedPointer(incoming.CoverMediaID, mediaRemap)
	}
	if choices["pinned_post_id"] == MergeUseIncoming {
		site.PinnedPostID = remappedPointer(incoming.PinnedPostID, entryRemap)
	}
}

func remappedPointer(value *string, remap map[string]string) *string {
	if value == nil {
		return nil
	}
	id := remap[*value]
	if id == "" {
		id = *value
	}
	return &id
}

func referencedMediaIDs(authority PortableAuthority) map[string]struct{} {
	result := map[string]struct{}{}
	for _, id := range []*string{authority.Site.AvatarMediaID, authority.Site.CoverMediaID} {
		if id != nil {
			result[*id] = struct{}{}
		}
	}
	for _, account := range authority.Users {
		for _, id := range []*string{account.AvatarMediaID, account.CoverMediaID} {
			if id != nil {
				result[*id] = struct{}{}
			}
		}
	}
	for _, entry := range authority.Entries {
		for _, usage := range entry.Media {
			result[usage.MediaID] = struct{}{}
		}
	}
	for _, comment := range authority.Comments {
		if comment.AuthorSnapshot != nil && comment.AuthorSnapshot.AvatarMediaID != nil {
			result[*comment.AuthorSnapshot.AvatarMediaID] = struct{}{}
		}
		for _, usage := range comment.Media {
			result[usage.MediaID] = struct{}{}
		}
	}
	return result
}

// SelectionDigest is persisted with the plan so replay and audit logs can
// identify the exact choices without storing secrets.
func SelectionDigest(selection MergeSelection) (string, error) {
	canonical := selection
	sort.Slice(canonical.Items, func(i, j int) bool {
		return mergeChoiceKey(canonical.Items[i].Kind, canonical.Items[i].ID) < mergeChoiceKey(canonical.Items[j].Kind, canonical.Items[j].ID)
	})
	sort.Slice(canonical.SiteFields, func(i, j int) bool { return canonical.SiteFields[i].Field < canonical.SiteFields[j].Field })
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload)), nil
}

func rebuildResolvedMergeGeneration(ctx context.Context, parent *runtimefs.Runtime, generation runtimefs.Generation, resolved ResolvedMerge, localRoot, incomingRoot string) (resultErr error) {
	stagingRuntime := &runtimefs.Runtime{Root: parent.Root, Instance: parent.Instance, Current: generation, Locks: parent.Locks}
	database, err := sqlitestore.Open(ctx, stagingRuntime, sqlitestore.Options{})
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, database.Close())
		}
	}()
	importer, err := database.BeginPortableImport(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, importer.Close())
		}
	}()

	mediaValues := sortedMedia(resolved.Authority.Media)
	for _, value := range mediaValues {
		source, exists := resolved.MediaSources[value.ID]
		if !exists {
			return fmt.Errorf("missing merge media source %s", value.ID)
		}
		storagePath, err := portableStoragePath(value.SHA256, value.MIMEType)
		if err != nil {
			return err
		}
		value.StoragePath = storagePath
		if err := importer.InsertMedia(ctx, value); err != nil {
			return err
		}
		sourceRoot := localRoot
		if source.Origin == MergeMediaIncoming {
			sourceRoot = incomingRoot
		}
		if err := copyResolvedOriginal(ctx, sourceRoot, source.Media, generation, value); err != nil {
			return err
		}
	}
	for _, value := range resolved.Authority.Users {
		if err := importer.InsertUser(ctx, value); err != nil {
			return err
		}
	}

	for _, value := range resolved.Authority.Media {
		for _, grant := range value.Grants {
			if err := importer.InsertMediaGrant(ctx, value.ID, grant); err != nil {
				return err
			}
		}
	}
	for _, value := range resolved.Authority.Identities {
		if err := importer.InsertIdentity(ctx, value); err != nil {
			return err
		}
	}
	for _, value := range sortedEntries(resolved.Authority.Entries) {
		if err := importer.InsertEntry(ctx, value); err != nil {
			return err
		}
	}
	for _, value := range resolved.Authority.Comments {
		if err := importer.InsertComment(ctx, value); err != nil {
			return err
		}
	}
	if err := importer.SetSite(ctx, resolved.Authority.Site); err != nil {
		return err
	}
	for _, record := range resolved.Authority.Interactions {
		if err := importer.InsertInteraction(ctx, record); err != nil {
			return err
		}
	}
	expected := sqlitestore.ArchiveCounts{
		Entries: int64(len(resolved.Authority.Entries)), Comments: int64(len(resolved.Authority.Comments)),
		Media: int64(len(resolved.Authority.Media)), Users: int64(len(resolved.Authority.Users)),
		Identities:   int64(len(resolved.Authority.Identities)),
		Interactions: int64(len(resolved.Authority.Interactions)),
	}
	if err := importer.Commit(ctx, expected); err != nil {
		return err
	}
	committed = true
	if err := database.Close(); err != nil {
		return err
	}
	closed = true
	verified, err := sqlitestore.Open(ctx, stagingRuntime, sqlitestore.Options{})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, verified.Close()) }()
	if err := verified.ValidatePortableReady(ctx, expected); err != nil {
		return err
	}
	authority, err := ReadPortableAuthority(ctx, verified)
	if err != nil {
		return err
	}
	want, err := AuthorityDigest(resolved.Authority)
	if err != nil {
		return err
	}
	got, err := AuthorityDigest(authority)
	if err != nil || got != want {
		return errors.Join(errors.New("rebuilt merge authority digest does not match resolved graph"), err)
	}
	return nil
}

func copyResolvedOriginal(ctx context.Context, sourceRoot string, source sqlitestore.ArchiveMedia, generation runtimefs.Generation, output sqlitestore.ArchiveMedia) (resultErr error) {
	if sourceRoot == "" || source.StoragePath == "" || output.StoragePath == "" {
		return errors.New("merge media source and destination are required")
	}
	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(source.StoragePath))
	relative, err := filepath.Rel(sourceRoot, sourcePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("merge media source escapes its generation")
	}
	before, err := os.Lstat(sourcePath)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() != source.ByteSize {
		return errors.Join(errors.New("merge media source is unsafe"), err)
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return errors.Join(errors.New("merge media source changed while opening"), err)
	}
	destination := filepath.Join(generation.Root, filepath.FromSlash(output.StoragePath))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	partial, err := os.CreateTemp(filepath.Dir(destination), ".merge-original-*")
	if err != nil {
		return err
	}
	partialPath := partial.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(partialPath)
		}
	}()
	copied, copyErr := io.CopyBuffer(partial, io.LimitReader(contextReader{ctx: ctx, reader: input}, output.ByteSize+1), make([]byte, copyBufferSize))
	if copyErr != nil || copied != output.ByteSize {
		_ = partial.Close()
		return errors.Join(copyErr, errors.New("merge media copy did not match declared size"))
	}
	if err := partial.Sync(); err != nil {
		_ = partial.Close()
		return err
	}
	if _, err := partial.Seek(0, io.SeekStart); err != nil {
		_ = partial.Close()
		return err
	}
	id, err := domain.ParseID(output.ID)
	if err != nil {
		_ = partial.Close()
		return err
	}
	if err := media.VerifyPortableOriginal(ctx, partial, media.Media{
		ID: id, SHA256: output.SHA256, StoragePath: output.StoragePath, OriginalName: output.OriginalName,
		MIMEType: output.MIMEType, ByteSize: output.ByteSize, Width: output.Width, Height: output.Height,
		CreatedAt: time.UnixMilli(output.CreatedAtMS).UTC(),
	}); err != nil {
		_ = partial.Close()
		return err
	}
	if err := partial.Close(); err != nil {
		return err
	}
	if err := runtimefs.PublishPreparedFileNoReplace(partialPath, destination); err != nil {
		return err
	}
	published = true
	return runtimefs.SyncDirectory(filepath.Dir(destination))
}
