package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
)

func TestFormulaArticleSurvivesPublishAndReopen(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	_, service := articleServiceFixture(t, time.UnixMilli(1000).UTC())
	source := []byte(`{"schema_version":4,"type":"doc","content":[{"type":"blockMath","attrs":{"latex":"\\sum_{k=1}^{n} k"}}]}`)
	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{Title: "Formula", BodyJSON: source})
	if err != nil {
		t.Fatal(err)
	}
	post, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(post.ContentHTML, `data-latex="\sum_{k=1}^{n} k"`) {
		t.Fatalf("missing rendered source: %s", post.ContentHTML)
	}
	reopened, err := service.EditArticle(ctx, post.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Draft.Body.SchemaVersion != 4 || reopened.Draft.Body.Content[0].Latex != `\sum_{k=1}^{n} k` {
		t.Fatalf("formula changed on reopen: %+v", reopened.Draft.Body)
	}
}
