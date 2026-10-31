package archive

import (
	"errors"
	"reflect"
	"sort"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func sortedInteractions(values []sqlitestore.ArchiveInteraction) []sqlitestore.ArchiveInteraction {
	result := append(make([]sqlitestore.ArchiveInteraction, 0, len(values)), values...)
	sort.Slice(result, func(i, j int) bool { return interactionKey(result[i]) < interactionKey(result[j]) })
	return result
}

// Imported activity is history, while local view positions remain authoritative.
func mergeInteractions(local, incoming PortableAuthority, resolved ResolvedMerge, choices map[string]MergeAction, newID func() (string, error)) ([]sqlitestore.ArchiveInteraction, error) {
	records := make(map[string]sqlitestore.ArchiveInteraction)
	var sequence int64
	for _, r := range local.Interactions {
		records[interactionKey(r)] = r
		if r.Notification != nil && r.Notification.Sequence > sequence {
			sequence = r.Notification.Sequence
		}
	}
	selected := make(map[domain.ContentRef]bool)
	for _, entry := range incoming.Entries {
		choice := choices[mergeChoiceKey(MergeEntry, entry.ID)]
		if choice == MergeAdd || choice == MergeUseIncoming || choice == MergeKeepBoth {
			selected[domain.ContentRef{Kind: domain.SubjectPost, ID: domain.ID(resolved.EntryRemap[entry.ID])}] = true
		}
	}
	for _, comment := range incoming.Comments {
		choice := choices[mergeChoiceKey(MergeComment, comment.ID)]
		if choice == MergeAdd || choice == MergeUseIncoming || choice == MergeKeepBoth {
			selected[domain.ContentRef{Kind: domain.SubjectComment, ID: domain.ID(resolved.CommentRemap[comment.ID])}] = true
		}
	}
	for key, r := range records {
		kind := ""
		switch r.Kind {
		case "post_mention":
			kind = domain.SubjectPost
		case "comment_mention", "reply_recipient":
			kind = domain.SubjectComment
		}
		if kind != "" && selected[domain.ContentRef{Kind: kind, ID: domain.ID(r.SubjectID)}] {
			delete(records, key)
		}
	}
	entries := indexEntries(resolved.Authority.Entries)
	comments := indexComments(resolved.Authority.Comments)
	values := append([]sqlitestore.ArchiveInteraction(nil), incoming.Interactions...)
	sort.SliceStable(values, func(i, j int) bool {
		a, b := values[i].Notification, values[j].Notification
		if a == nil || b == nil {
			return a == nil && b != nil
		}
		return a.Sequence < b.Sequence
	})
	for _, r := range values {
		if r.Kind == "seen" {
			continue
		}
		if r.Notification != nil {
			n := *r.Notification
			originalSubject, originalPost := n.Subject, n.PostID
			if id, ok := resolved.EntryRemap[n.PostID.String()]; ok {
				n.PostID = domain.ID(id)
			} else if _, ok := entries[n.PostID.String()]; !ok {
				n.ForceUnavailable = true
			}
			mapping := resolved.EntryRemap
			if n.Subject.Kind == domain.SubjectComment {
				mapping = resolved.CommentRemap
			}
			if id, ok := mapping[n.Subject.ID.String()]; ok {
				n.Subject.ID = domain.ID(id)
			} else {
				if n.Subject.Kind == domain.SubjectComment {
					if _, ok := comments[n.Subject.ID.String()]; !ok {
						n.ForceUnavailable = true
					}
				} else if _, ok := entries[n.Subject.ID.String()]; !ok {
					n.ForceUnavailable = true
				}
			}
			if n.ReplyToCommentID != nil {
				if id, ok := resolved.CommentRemap[n.ReplyToCommentID.String()]; ok {
					v := domain.ID(id)
					n.ReplyToCommentID = &v
				}
			}
			key := interactionKey(r)
			if existing, ok := records[key]; ok {
				if originalSubject != n.Subject || originalPost != n.PostID {
					id, err := newID()
					if err != nil {
						return nil, err
					}
					n.ID = domain.ID(id)
				} else {
					left, right := *existing.Notification, n
					left.Sequence, right.Sequence = 0, 0
					left.Historical, right.Historical = false, false
					left.ForceUnavailable, right.ForceUnavailable = false, false
					left.ActorID, right.ActorID = nil, nil
					left.ReplyToCommentID, right.ReplyToCommentID = nil, nil
					if !reflect.DeepEqual(left, right) {
						return nil, errors.New("notification identity conflicts with existing immutable history")
					}
					continue
				}
			}
			sequence++
			n.Sequence = sequence
			n.Historical = true
			r.Notification = &n
			records[interactionKey(r)] = r
			continue
		}
		mapping, kind := resolved.EntryRemap, domain.SubjectPost
		if r.Kind == "comment_like" || r.Kind == "comment_mention" || r.Kind == "reply_recipient" {
			mapping, kind = resolved.CommentRemap, domain.SubjectComment
		}
		id, ok := mapping[r.SubjectID]
		if !ok {
			continue
		}
		r.SubjectID = id
		if r.Kind == "post_mention" || r.Kind == "comment_mention" || r.Kind == "reply_recipient" {
			if !selected[domain.ContentRef{Kind: kind, ID: domain.ID(id)}] {
				continue
			}
		}
		key := interactionKey(r)
		if _, exists := records[key]; !exists {
			records[key] = r
		}
	}
	result := make([]sqlitestore.ArchiveInteraction, 0, len(records))
	for _, r := range records {
		result = append(result, r)
	}
	return sortedInteractions(result), nil
}
