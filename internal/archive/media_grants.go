package archive

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"sort"
)

var portableMediaFields = []string{"id", "sha256", "archive_path", "original_name", "mime_type", "byte_size", "width", "height", "created_at", "grants"}

func archiveMediaGrants(values []mediaGrantRecord) ([]sqlitestore.ArchiveMediaGrant, error) {
	if values == nil {
		return nil, errors.New("media grants must be an array")
	}
	result := make([]sqlitestore.ArchiveMediaGrant, 0, len(values))
	last := ""
	for _, value := range values {
		if domain.ValidateUserID(value.UserID) != nil || value.UserID <= last {
			return nil, errors.New("media grants must name unique accounts in ID order")
		}
		at, err := parsePortableTimestamp(value.CreatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, sqlitestore.ArchiveMediaGrant{UserID: value.UserID, CreatedAtMS: at.UnixMilli()})
		last = value.UserID
	}
	return result, nil
}

func validatePortableMediaGrants(ctx context.Context, files map[string]*zip.File) error {
	users := map[string]bool{}
	if _, err := validateJSONLines[userRecord](ctx, files["data/users.jsonl"], portableUserFields, func(value userRecord, _ map[string]json.RawMessage) error { users[value.ID] = true; return nil }); err != nil {
		return err
	}
	_, err := validateJSONLines[mediaRecord](ctx, files["data/media.jsonl"], portableMediaFields, func(value mediaRecord, _ map[string]json.RawMessage) error {
		grants, err := archiveMediaGrants(value.Grants)
		if err != nil {
			return err
		}
		for _, grant := range grants {
			if !users[grant.UserID] {
				return errors.New("media grant refers to an unknown account")
			}
		}
		return nil
	})
	return err
}

// Merging content unions reuse permissions for the same immutable bytes. Account
// identity remains unchanged, including when the incoming media ID is remapped.
func mergeMediaPermissions(resolved *PortableAuthority, local, incoming PortableAuthority, remap map[string]string) {
	users := map[string]bool{}
	for _, user := range resolved.Users {
		users[user.ID] = true
	}
	grants := map[string]map[string]int64{}
	add := func(id string, values []sqlitestore.ArchiveMediaGrant) {
		if grants[id] == nil {
			grants[id] = map[string]int64{}
		}
		for _, value := range values {
			if users[value.UserID] {
				old, exists := grants[id][value.UserID]
				if !exists || value.CreatedAtMS < old {
					grants[id][value.UserID] = value.CreatedAtMS
				}
			}
		}
	}
	for _, value := range local.Media {
		add(value.ID, value.Grants)
	}
	for _, value := range incoming.Media {
		if id, exists := remap[value.ID]; exists {
			add(id, value.Grants)
		}
	}
	for index := range resolved.Media {
		values := make([]sqlitestore.ArchiveMediaGrant, 0, len(grants[resolved.Media[index].ID]))
		for user, at := range grants[resolved.Media[index].ID] {
			values = append(values, sqlitestore.ArchiveMediaGrant{UserID: user, CreatedAtMS: at})
		}
		sort.Slice(values, func(i, j int) bool { return values[i].UserID < values[j].UserID })
		resolved.Media[index].Grants = values
	}
}
