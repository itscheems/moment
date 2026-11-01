package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
)

func TestSessionSweepPurgesStaleBrowserCollectionsWithoutEndingLiveSessions(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	ctx := context.Background()
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour).UnixMilli()
	const userID = "11111111111111111111111111111111"
	const liveSession = "22222222222222222222222222222222"
	const expiredSession = "33333333333333333333333333333333"
	if err := database.WriteTx(ctx, func(queries Queries) error {
		if _, err := queries.ExecContext(ctx, `INSERT INTO users(id,username,display_name,role,status,created_at_ms,updated_at_ms) VALUES (?, 'retention_member', 'Retention Member', 'admin', 'active', 1, 1)`, userID); err != nil {
			return err
		}
		for _, session := range []struct {
			id      string
			expires int64
		}{{liveSession, now.Add(time.Hour).UnixMilli()}, {expiredSession, cutoff - 1}} {
			if _, err := queries.ExecContext(ctx, `INSERT INTO sessions(id,user_id,issued_at_ms,expires_at_ms,last_seen_at_ms,surface) VALUES (?,?,1,?,1,'web')`, session.id, userID, session.expires); err != nil {
				return err
			}
		}
		for _, set := range []struct {
			id      string
			expires int64
			revoked any
		}{
			{"expired-old", cutoff - 1, nil},
			{"expired-boundary", cutoff, nil},
			{"expired-recent", cutoff + 1, nil},
			{"revoked-old", now.Add(time.Hour).UnixMilli(), cutoff - 1},
			{"revoked-boundary", now.Add(time.Hour).UnixMilli(), cutoff},
			{"revoked-recent", now.Add(time.Hour).UnixMilli(), cutoff + 1},
			{"live", now.Add(time.Hour).UnixMilli(), nil},
		} {
			if _, err := queries.ExecContext(ctx, `INSERT INTO browser_account_sets(id,credential_hash,csrf_token,session_epoch,expires_at_ms,revoked_at_ms) VALUES (?,randomblob(32),'csrf',1,?,?)`, set.id, set.expires, set.revoked); err != nil {
				return err
			}
			if _, err := queries.ExecContext(ctx, `INSERT INTO browser_account_members(set_id,user_id,session_id,last_used_at_ms) VALUES (?,?,?,1)`, set.id, userID, liveSession); err != nil {
				return err
			}
		}
		_, err := queries.ExecContext(ctx, `CREATE TRIGGER fail_collection_purge BEFORE DELETE ON browser_account_sets BEGIN SELECT RAISE(ABORT, 'collection purge unavailable'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository, app.WithAccountClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SweepExpiredSessions(ctx); err == nil {
		t.Fatal("collection purge failure was ignored")
	}
	assertCount(t, database.read, `SELECT count(*) FROM sessions`, 2)
	assertCount(t, database.read, `SELECT count(*) FROM browser_account_sets`, 7)
	if err := database.WriteTx(ctx, func(queries Queries) error {
		_, err := queries.ExecContext(ctx, `DROP TRIGGER fail_collection_purge`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := service.SweepExpiredSessions(ctx)
	if err != nil || removed != 1 {
		t.Fatalf("session count changed: removed=%d, err=%v", removed, err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM browser_account_sets WHERE id IN ('expired-old','revoked-old')`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM browser_account_sets`, 5)
	assertCount(t, database.read, `SELECT count(*) FROM browser_account_members`, 5)
	if _, err := service.ResolveSession(ctx, liveSession); err != nil {
		t.Fatalf("collection purge ended a retained normal session: %v", err)
	}
}
