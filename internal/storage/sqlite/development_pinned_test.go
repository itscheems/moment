package sqlite

import (
	"strings"
	"testing"
	"time"
)

func TestPinnedSQLiteDSNUsesCanonicalWindowsFileURL(t *testing.T) {
	if got := normalizedFileURLPath(`C:\Moment\generations\dev\moment.db`, "windows"); got != "/C:/Moment/generations/dev/moment.db" {
		t.Fatalf("normalized Windows SQLite path = %q", got)
	}
	dsn := pinnedSQLiteDSN(normalizedFileURLPath(`C:\Moment\generations\dev\moment.db`, "windows"), time.Second, false)
	if !strings.HasPrefix(dsn, "file:///C:/Moment/generations/dev/moment.db?") {
		t.Fatalf("pinned Windows SQLite DSN = %q", dsn)
	}
}
