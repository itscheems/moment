package domain

import "testing"

func videoCard() LinkCard {
	duration := 212
	return LinkCard{
		Position:         0,
		SourceURL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		CanonicalURL:     "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Provider:         LinkProviderYouTube,
		Kind:             LinkKindVideo,
		Title:            "a title",
		SiteName:         "YouTube",
		DurationSeconds:  &duration,
		ThumbnailMediaID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		EmbedURL:         "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		CapturedAt:       1_700_000_000_000,
	}
}

func TestLinkCardAcceptsCapturedVideoSnapshot(t *testing.T) {
	if err := videoCard().Validate(); err != nil {
		t.Fatalf("expected a captured video card to validate: %v", err)
	}
}

func TestLinkCardRejectsUnsafeAndUnnormalizedURLs(t *testing.T) {
	for name, mutate := range map[string]func(*LinkCard){
		"credentials":   func(card *LinkCard) { card.CanonicalURL = "https://user:pass@example.com/a" },
		"non http":      func(card *LinkCard) { card.CanonicalURL = "javascript:alert(1)" },
		"fragment kept": func(card *LinkCard) { card.CanonicalURL = "https://example.com/a#top" },
		"missing host":  func(card *LinkCard) { card.CanonicalURL = "https:///a" },
		"empty source":  func(card *LinkCard) { card.SourceURL = "" },
	} {
		card := videoCard()
		mutate(&card)
		if err := card.Validate(); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestLinkCardRejectsEmbedURLOnPlainLink(t *testing.T) {
	card := videoCard()
	card.Kind = LinkKindLink
	if err := card.Validate(); err == nil {
		t.Fatal("expected a plain link card to reject an embed URL")
	}
	card.EmbedURL = ""
	card.DurationSeconds = nil
	if err := card.Validate(); err != nil {
		t.Fatalf("expected a plain link card without an embed URL to validate: %v", err)
	}
}

func TestLinkCardRejectsForeignThumbnailReference(t *testing.T) {
	card := videoCard()
	card.ThumbnailMediaID = "https://i.ytimg.com/vi/x/maxresdefault.jpg"
	if err := card.Validate(); err == nil {
		t.Fatal("expected a non-managed thumbnail reference to be rejected")
	}
}

func TestValidateLinkCardsEnforcesMomentCeilingAndOrder(t *testing.T) {
	first := videoCard()
	second := videoCard()
	second.Position = 1
	if err := ValidateLinkCards([]LinkCard{first, second}); err == nil {
		t.Fatal("expected more than one card on a Moment to be rejected")
	}
	misplaced := videoCard()
	misplaced.Position = 3
	if err := ValidateLinkCards([]LinkCard{misplaced}); err == nil {
		t.Fatal("expected a non-zero starting position to be rejected")
	}
	if err := ValidateLinkCards([]LinkCard{first}); err != nil {
		t.Fatalf("expected one ordered card to validate: %v", err)
	}
	if err := ValidateLinkCards(nil); err != nil {
		t.Fatalf("expected no cards to validate: %v", err)
	}
}

func TestNormalizeLinkURLStripsFragmentAndLowercasesScheme(t *testing.T) {
	normalized, err := NormalizeLinkURL("  HTTPS://www.Example.com/watch?v=1#frag ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if normalized != "https://www.Example.com/watch?v=1" {
		t.Fatalf("unexpected normalization: %q", normalized)
	}
}

func TestCloneLinkCardsCopiesDuration(t *testing.T) {
	cards := []LinkCard{videoCard()}
	clones := CloneLinkCards(cards)
	*clones[0].DurationSeconds = 1
	if *cards[0].DurationSeconds == 1 {
		t.Fatal("expected the duration pointer to be deep copied")
	}
}
