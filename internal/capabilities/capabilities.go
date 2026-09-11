// Package capabilities assembles client-visible limits from their owning packages.
package capabilities

import (
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/mediatype"
)

// Description is an additive HTTP/CLI contract. Lengths count Unicode code points.
type Description struct {
	Version        int    `json:"version"`
	TextLengthUnit string `json:"text_length_unit"`
	Limits         Limits `json:"limits"`
}

// Limits describes effective ingestion ceilings and canonical content budgets.
type Limits struct {
	MaxImageBytes           int64    `json:"max_image_bytes"`
	MaxVideoBytes           int64    `json:"max_video_bytes"`
	MaxPostMediaBytes       int64    `json:"max_post_media_bytes"`
	MaxCommentMediaBytes    int64    `json:"max_comment_media_bytes"`
	MaxPostMedia            int      `json:"max_post_media"`
	MaxCommentMedia         int      `json:"max_comment_media"`
	MaxVideoDurationSeconds int64    `json:"max_video_duration_seconds"`
	MaxImageDimension       uint32   `json:"max_image_dimension"`
	MaxImagePixels          uint64   `json:"max_image_pixels"`
	PreviewMaxEdge          uint32   `json:"preview_max_edge"`
	MaxPostCodePoints       int      `json:"max_post_code_points"`
	MaxCommentCodePoints    int      `json:"max_comment_code_points"`
	MaxAltCodePoints        int      `json:"max_alt_code_points"`
	DefaultPageSize         int      `json:"default_page_size"`
	MaxPageSize             int      `json:"max_page_size"`
	MaxCursorBytes          int      `json:"max_cursor_bytes"`
	DefaultCommentPageSize  int      `json:"default_comment_page_size"`
	DefaultReplyPageSize    int      `json:"default_reply_page_size"`
	InlineReplyPreviewCount int      `json:"inline_reply_preview_count"`
	AcceptedImageTypes      []string `json:"accepted_image_types"`
	AcceptedVideoTypes      []string `json:"accepted_video_types"`
	MaxPostLinkCards        int      `json:"max_post_link_cards"`
}

// Describe uses the actual media store configuration; nil selects product defaults.
func Describe(store *media.Store) Description {
	effective := store.EffectiveLimits()
	limits := Limits{
		MaxImageBytes: effective.MaxImageBytes, MaxVideoBytes: effective.MaxVideoBytes,
		MaxPostMediaBytes: domain.MaxEntryMediaBytes, MaxCommentMediaBytes: domain.MaxCommentMediaBytes,
		MaxPostMedia: domain.MaxEntryMediaUsages, MaxCommentMedia: domain.MaxCommentMediaUsages,
		MaxVideoDurationSeconds: int64(media.DefaultMaxVideoDuration.Seconds()),
		MaxImageDimension:       effective.MaxDimension, MaxImagePixels: effective.MaxPixels, PreviewMaxEdge: effective.PreviewMaxEdge,
		MaxPostCodePoints: domain.MaxContentCodePoints, MaxCommentCodePoints: domain.MaxCommentContentCodePoints, MaxAltCodePoints: domain.MaxMediaAltCodePoints,
		DefaultPageSize: app.DefaultPostPageSize, MaxPageSize: app.MaxPostPageSize, MaxCursorBytes: app.MaxCursorBytes,
		DefaultCommentPageSize: app.DefaultCommentPageSize, DefaultReplyPageSize: app.DefaultReplyPageSize, InlineReplyPreviewCount: app.InlineReplyPreviewCount,
		AcceptedImageTypes: []string{}, AcceptedVideoTypes: []string{}, MaxPostLinkCards: domain.MaxMomentLinkCards,
	}
	for _, item := range mediatype.All() {
		if item.Kind == mediatype.Video {
			limits.AcceptedVideoTypes = append(limits.AcceptedVideoTypes, item.MIMEType)
		} else {
			limits.AcceptedImageTypes = append(limits.AcceptedImageTypes, item.MIMEType)
		}
	}
	return Description{Version: 1, TextLengthUnit: "unicode_code_points", Limits: limits}
}

// Valid checks the complete required client contract without comparing remote
// values to local constants: a different server may enforce different limits.
func (description Description) Valid() bool {
	if description.Version != 1 || description.TextLengthUnit != "unicode_code_points" {
		return false
	}
	l := description.Limits
	for _, value := range []int64{l.MaxImageBytes, l.MaxVideoBytes, l.MaxPostMediaBytes, l.MaxCommentMediaBytes, int64(l.MaxPostMedia), int64(l.MaxCommentMedia), l.MaxVideoDurationSeconds, int64(l.MaxImageDimension), int64(l.MaxImagePixels), int64(l.PreviewMaxEdge), int64(l.MaxPostCodePoints), int64(l.MaxCommentCodePoints), int64(l.MaxAltCodePoints), int64(l.DefaultPageSize), int64(l.MaxPageSize), int64(l.MaxCursorBytes), int64(l.DefaultCommentPageSize), int64(l.DefaultReplyPageSize), int64(l.InlineReplyPreviewCount), int64(l.MaxPostLinkCards)} {
		if value <= 0 {
			return false
		}
	}
	return l.DefaultPageSize <= l.MaxPageSize && l.DefaultCommentPageSize <= l.MaxPageSize && l.DefaultReplyPageSize <= l.MaxPageSize && len(l.AcceptedImageTypes) > 0 && len(l.AcceptedVideoTypes) > 0
}
