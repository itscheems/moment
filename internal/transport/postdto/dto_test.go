package postdto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestPostMediaReadProjectionCarriesStrictPositiveDimensions(t *testing.T) {
	id, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	wire := FromPost(app.Post{
		Entry: domain.Entry{
			ID: id, Kind: domain.KindMoment, Visibility: domain.VisibilityEveryone, ContentMD: "with image",
			CreatedAt: instant, UpdatedAt: instant, PublishedAt: instant,
		},
		ContentHTML: "<p>with image</p>\n",
		Media:       []app.MediaUsageProjection{{MediaID: mediaID, Position: 0, MIMEType: "image/jpeg", Width: 1200, Height: 800}},
	})
	if err := ValidatePost(wire); err != nil {
		t.Fatal(err)
	}
	if wire.TimelineKind != string(app.TimelineItemOriginal) {
		t.Fatalf("canonical timeline kind = %q", wire.TimelineKind)
	}
	encoded, err := json.Marshal(wire.Media[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 || fields["mime_type"] != "image/jpeg" || fields["width"] != float64(1200) || fields["height"] != float64(800) {
		t.Fatalf("media projection = %s", encoded)
	}

	for name, raw := range map[string]string{
		"missing width": `{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","position":0,"alt_text":null,"mime_type":"image/jpeg","height":800}`,
		"unknown field": `{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","position":0,"alt_text":null,"mime_type":"image/jpeg","width":1200,"height":800,"extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var projection MediaProjection
			if err := json.Unmarshal([]byte(raw), &projection); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}

	wire.Media[0].Height = 0
	if err := ValidatePost(wire); err == nil {
		t.Fatal("accepted zero media height")
	}
}

func TestRepostTimelineProjectionRequiresActiveRepost(t *testing.T) {
	instant := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	id, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	wire := FromPost(app.Post{
		Entry: domain.Entry{
			ID: id, Kind: domain.KindMoment, Visibility: domain.VisibilityEveryone, ContentMD: "reposted",
			CreatedAt: instant, UpdatedAt: instant, PublishedAt: instant,
		},
		TimelineKind: app.TimelineItemRepost,
		ContentHTML:  "<p>reposted</p>\n",
		Media:        []app.MediaUsageProjection{},
	})
	if err := ValidatePost(wire); err == nil {
		t.Fatal("accepted repost projection without reposted_at")
	}
	repostedAt := instant.Add(time.Minute)
	wire.RepostedAt = &repostedAt
	wire.UpdatedAt = repostedAt
	if err := ValidatePost(wire); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableQuoteUsesAnExplicitNullOnlyTombstone(t *testing.T) {
	instant := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	id, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	wire := FromPost(app.Post{
		Entry: domain.Entry{
			ID: id, Kind: domain.KindMoment, Visibility: domain.VisibilityEveryone, ContentMD: "保留引用外层",
			CreatedAt: instant, UpdatedAt: instant, PublishedAt: instant,
		},
		ContentHTML: "<p>保留引用外层</p>\n",
		Media:       []app.MediaUsageProjection{},
		Quoted:      &app.QuotedPost{Unavailable: true},
	})
	if err := ValidatePost(wire); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire.QuotedPost)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 7 || fields["unavailable"] != true || fields["id"] != nil || fields["content_md"] != nil || fields["content_html"] != nil || fields["published_at"] != nil || fields["media"] != nil || fields["location"] != nil {
		t.Fatalf("quote tombstone = %s", encoded)
	}

	wire.QuotedPost.ID = &wire.ID
	if err := ValidatePost(wire); err == nil {
		t.Fatal("accepted unavailable quote that exposed target data")
	}
}

func TestMediaUsageWriteShapeRemainsDimensionFree(t *testing.T) {
	encoded, err := json.Marshal(MediaUsage{MediaID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Position: 0})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("write media shape = %s", encoded)
	}
}
