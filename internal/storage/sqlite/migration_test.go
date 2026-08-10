package sqlite

import "testing"

func TestMigrationChecksumCanonicalizesLineEndings(t *testing.T) {
	lf := "CREATE TABLE example (id INTEGER);\n-- next statement\nSELECT 1;\n"
	wantSQL, wantChecksum := canonicalMigration([]byte(lf))

	for name, contents := range map[string]string{
		"LF":      lf,
		"CRLF":    "CREATE TABLE example (id INTEGER);\r\n-- next statement\r\nSELECT 1;\r\n",
		"lone CR": "CREATE TABLE example (id INTEGER);\r-- next statement\rSELECT 1;\r",
		"mixed":   "CREATE TABLE example (id INTEGER);\r\n-- next statement\rSELECT 1;\n",
	} {
		t.Run(name, func(t *testing.T) {
			actualSQL, actualChecksum := canonicalMigration([]byte(contents))
			if actualSQL != wantSQL {
				t.Fatalf("canonical SQL = %q, want %q", actualSQL, wantSQL)
			}
			if actualChecksum != wantChecksum {
				t.Fatalf("checksum = %q, want %q", actualChecksum, wantChecksum)
			}
		})
	}
}
