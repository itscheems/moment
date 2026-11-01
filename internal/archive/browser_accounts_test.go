package archive

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestPortableRestoreDoesNotCarryRetainedBrowserAccess(t *testing.T) {
	fixture := newExportFixture(t, true)
	ctx := context.Background()
	repository, err := sqlitestore.NewAccountRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := strings.Repeat("a", 32)
	if _, err := accounts.StartSession(ctx, owner.ID, sessionID, "web", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	secret := "browser-export-secret-" + strings.Repeat("p", 32)
	csrf := "browser-export-csrf-must-not-leak"
	set, err := accounts.BootstrapBrowserAccounts(ctx, secret, csrf, 1, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	contents := exportFixtureBytes(t, fixture)
	members, _ := readArchive(t, contents)
	for name, body := range members {
		for _, sensitive := range []string{secret, csrf, sessionID, set.ID} {
			if bytes.Contains(body, []byte(sensitive)) {
				t.Fatalf("browser access escaped in %s", name)
			}
		}
	}
	database := stageArchive(t, fixture, exportFixtureFile(t, fixture))
	if err := database.ReadTx(ctx, func(q sqlitestore.Queries) error {
		var sets, members int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM browser_account_sets`).Scan(&sets); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM browser_account_members`).Scan(&members); err != nil {
			return err
		}
		if sets != 0 || members != 0 {
			t.Fatal("portable restore retained browser authority")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
