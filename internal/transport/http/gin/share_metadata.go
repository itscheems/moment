package ginhttp

import (
	"bytes"
	"html"
	"strings"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const (
	shareMetadataStart = "<!-- moment:share-meta:start -->"
	shareMetadataEnd   = "<!-- moment:share-meta:end -->"
)

func shareMetadataForRequest(ctx *gin.Context, service *app.ShareMetadataService) (app.ShareMetadata, bool) {
	if service == nil {
		return app.ShareMetadata{}, false
	}
	requestPath := ctx.Request.URL.Path
	var (
		metadata app.ShareMetadata
		err      error
	)
	switch requestPath {
	case "/":
		metadata, err = service.Page(ctx.Request.Context(), app.SharePageTimeline)
	case "/search":
		metadata, err = service.Page(ctx.Request.Context(), app.SharePageSearch)
	case "/archive":
		metadata, err = service.Page(ctx.Request.Context(), app.SharePageArchive)
	default:
		segments := strings.Split(strings.TrimSuffix(strings.TrimPrefix(requestPath, "/"), "/"), "/")
		postPath := len(segments) == 2 && segments[0] == "m"
		articlePath := len(segments) == 3 && segments[1] == "article" && segments[0] != "compose"
		if postPath || articlePath {
			if id, parseErr := domain.ParseID(segments[len(segments)-1]); parseErr == nil {
				metadata, err = service.Post(ctx.Request.Context(), id)
			} else {
				metadata, err = service.Fallback(ctx.Request.Context())
			}
		} else {
			metadata, err = service.Fallback(ctx.Request.Context())
		}
	}
	if err != nil {
		return app.ShareMetadata{
			Title:       "Moment",
			Description: "记下此刻，找回后来。",
			Type:        "website",
			NoIndex:     true,
		}, true
	}
	return metadata, true
}

func injectShareMetadata(index []byte, metadata app.ShareMetadata) []byte {
	start := bytes.Index(index, []byte(shareMetadataStart))
	end := bytes.Index(index, []byte(shareMetadataEnd))
	if start < 0 || end < 0 || end < start {
		return index
	}
	end += len(shareMetadataEnd)
	block := renderShareMetadata(metadata)
	result := make([]byte, 0, len(index)-(end-start)+len(block))
	result = append(result, index[:start]...)
	result = append(result, block...)
	result = append(result, index[end:]...)
	return result
}

func renderShareMetadata(metadata app.ShareMetadata) []byte {
	escape := html.EscapeString
	var output strings.Builder
	writeMetaElement(&output, "title", "", metadata.Title)
	writeMetaElement(&output, "meta", `name="description"`, metadata.Description)
	writeMetaElement(&output, "meta", `property="og:title"`, metadata.Title)
	writeMetaElement(&output, "meta", `property="og:description"`, metadata.Description)
	writeMetaElement(&output, "meta", `property="og:type"`, metadata.Type)
	card := "summary"
	if metadata.ImageURL != "" {
		card = "summary_large_image"
	}
	writeMetaElement(&output, "meta", `name="twitter:card"`, card)
	writeMetaElement(&output, "meta", `name="twitter:title"`, metadata.Title)
	writeMetaElement(&output, "meta", `name="twitter:description"`, metadata.Description)
	if metadata.CanonicalURL != "" {
		output.WriteString(`<link rel="canonical" href="` + escape(metadata.CanonicalURL) + `" />` + "\n")
		writeMetaElement(&output, "meta", `property="og:url"`, metadata.CanonicalURL)
	}
	if metadata.ImageURL != "" {
		writeMetaElement(&output, "meta", `property="og:image"`, metadata.ImageURL)
		writeMetaElement(&output, "meta", `name="twitter:image"`, metadata.ImageURL)
	}
	if metadata.NoIndex {
		writeMetaElement(&output, "meta", `name="robots"`, "noindex")
	}
	return []byte(output.String())
}

func writeMetaElement(output *strings.Builder, element, attributes, value string) {
	escaped := html.EscapeString(value)
	if element == "title" {
		output.WriteString("<title>" + escaped + "</title>\n")
		return
	}
	output.WriteString("<" + element + " " + attributes + ` content="` + escaped + `" />` + "\n")
}
