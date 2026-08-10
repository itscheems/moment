package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const testID = ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")

func TestMarkdownContentBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		code    Code
	}{
		{name: "empty", content: " \n\t", code: CodeContentEmpty},
		{name: "invalid UTF-8", content: string([]byte{0xff}), code: CodeInvalidArgument},
		{name: "unicode over limit", content: strings.Repeat("瞬", MaxContentCodePoints+1), code: CodeContentTooLarge},
		{name: "raw inline html", content: "hello <b>world</b>", code: CodeInvalidArgument},
		{name: "raw html block", content: "<section>world</section>", code: CodeInvalidArgument},
		{name: "javascript link", content: "[bad](javascript:alert(1))", code: CodeInvalidArgument},
		{name: "data link", content: "[bad](data:text/html,hello)", code: CodeInvalidArgument},
		{name: "scheme relative", content: "[bad](//example.com/path)", code: CodeInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateMarkdown(test.content)
			if !HasCode(err, test.code) {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}

	if err := ValidateMarkdown(strings.Repeat("瞬", MaxContentCodePoints)); err != nil {
		t.Fatalf("exact Unicode limit should pass: %v", err)
	}
	if err := ValidateMarkdown("hello **Moment** [site](https://example.com) [local](/m/01A) `code`"); err != nil {
		t.Fatalf("safe Markdown should pass: %v", err)
	}
}

func TestEveryMarkdownImageFormIsRejected(t *testing.T) {
	t.Parallel()
	tests := []string{
		"![remote](https://example.com/a.png)",
		"![relative](../a.png)",
		"![data](data:image/png;base64,AA==)",
		"![reference][image]\n\n[image]: https://example.com/a.png",
		"[![nested](https://example.com/a.png)](https://example.com)",
	}
	for _, content := range tests {
		if err := ValidateMarkdown(content); !HasCode(err, CodeExternalImageNotAllowed) {
			t.Errorf("expected image rejection for %q, got %v", content, err)
		}
	}
}

func TestRenderMarkdownIsSafe(t *testing.T) {
	t.Parallel()
	html, err := RenderMarkdown("hello **Moment** [site](https://example.com)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<strong>Moment</strong>`) {
		t.Fatalf("expected formatted output, got %s", html)
	}
	if !strings.Contains(html, `rel="nofollow noopener noreferrer"`) {
		t.Fatalf("expected safe rel attributes, got %s", html)
	}
	if strings.Contains(html, "<img") {
		t.Fatalf("rendered output must not contain images: %s", html)
	}
}

func TestCommentIdentityAndMarkdownBoundaries(t *testing.T) {
	t.Parallel()
	name, err := NormalizeCommentAuthorName("  访客  ")
	if err != nil || name != "访客" {
		t.Fatalf("normalized name = %q, %v", name, err)
	}
	for _, value := range []string{"", " \n ", "bad\nname", strings.Repeat("名", MaxCommentAuthorNameCodePoints+1)} {
		if _, err := NormalizeCommentAuthorName(value); !HasCode(err, CodeInvalidArgument) {
			t.Fatalf("invalid name %q = %v", value, err)
		}
	}
	if err := ValidateCommentMarkdown(strings.Repeat("评", MaxCommentContentCodePoints)); err != nil {
		t.Fatalf("exact comment content limit: %v", err)
	}
	for _, content := range []string{strings.Repeat("评", MaxCommentContentCodePoints+1), "<script>alert(1)</script>", "![remote](https://example.test/a.png)"} {
		if err := ValidateCommentMarkdown(content); err == nil {
			t.Fatalf("unsafe comment content passed: %q", content)
		}
	}
	html, err := RenderCommentMarkdown("safe **reply** [link](https://example.test)")
	if err != nil || !strings.Contains(html, "<strong>reply</strong>") || !strings.Contains(html, "nofollow noopener noreferrer") {
		t.Fatalf("rendered comment = %q, %v", html, err)
	}
}

func TestEntryUpdatePreservesTimelinePosition(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, time.August, 10, 8, 9, 10, 987654321, time.FixedZone("source", 8*60*60))
	entry, err := NewEntry(testID, "first", created)
	if err != nil {
		t.Fatal(err)
	}
	originalCreated, originalPublished := entry.CreatedAt, entry.PublishedAt
	if err := entry.UpdateContent("edited", created.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if entry.CreatedAt != originalCreated || entry.PublishedAt != originalPublished {
		t.Fatal("editing moved the original timeline instants")
	}
	if entry.UpdatedAt != NormalizeTime(created.Add(time.Hour)) {
		t.Fatalf("unexpected updated time %s", entry.UpdatedAt)
	}
	if entry.CreatedAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatal("entry time was not normalized to millisecond precision")
	}
}

func TestIDValidationAndDeterministicGeneration(t *testing.T) {
	t.Parallel()
	if _, err := ParseID(testID.String()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", strings.ToLower(testID.String()), testID.String() + "0", "01ARZ3NDEKTSV4RRFFQ69G5FAI"} {
		if _, err := ParseID(invalid); !HasCode(err, CodeInvalidArgument) {
			t.Errorf("expected invalid id for %q, got %v", invalid, err)
		}
	}
	now := time.UnixMilli(1_700_000_000_000)
	first, err := NewID(now, bytes.NewReader(make([]byte, 10)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewID(now, bytes.NewReader(make([]byte, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 26 {
		t.Fatalf("expected deterministic canonical ULID, got %q and %q", first, second)
	}
	if _, err := NewID(now, nil); !HasCode(err, CodeInternal) {
		t.Fatalf("expected a missing entropy source to fail, got %v", err)
	}
}

func TestDayRangeRespectsDSTAndUTCMapping(t *testing.T) {
	t.Parallel()
	start, end, err := DayRange("2026-03-08", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := end.Sub(start); got != 23*time.Hour {
		t.Fatalf("spring DST day should be 23 hours, got %s", got)
	}
	start, end, err = DayRange("2026-11-01", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := end.Sub(start); got != 25*time.Hour {
		t.Fatalf("fall DST day should be 25 hours, got %s", got)
	}
	if _, _, err := DayRange("2026-02-30", "Asia/Shanghai"); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("expected invalid date, got %v", err)
	}
	if _, _, err := DayRange("2026-08-10", "Mars/Olympus"); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("expected invalid timezone, got %v", err)
	}

	instant := time.Date(2026, 8, 10, 12, 0, 0, 123456789, time.FixedZone("source", 8*60*60))
	millis := TimeToMillis(instant)
	if got := TimeFromMillis(millis); got.Location() != time.UTC || got.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("unexpected UTC millisecond mapping %s", got)
	}
	if !strings.HasSuffix(FormatTime(instant), "Z") {
		t.Fatalf("API time must be UTC: %s", FormatTime(instant))
	}
}
