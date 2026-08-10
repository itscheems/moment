// Package searchdto freezes the public search and archive wire shapes shared
// by Gin, CLI direct mode, and CLI API mode.
package searchdto

import (
	"errors"
	"sort"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/transport/postdto"
)

// Hit is the flattened public post and its safe snippet.
type Hit struct {
	postdto.Post
	Snippet string `json:"snippet"`
}

// YearCount is one complete search-result aggregate.
type YearCount struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// SearchData is the stable search response body.
type SearchData struct {
	Items      []Hit       `json:"items"`
	YearCounts []YearCount `json:"year_counts"`
}

// MonthCount is one non-empty archive month.
type MonthCount struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Count int `json:"count"`
}

// MonthsData is the stable archive month-count response.
type MonthsData struct {
	Items []MonthCount `json:"items"`
}

// DayCount is one non-empty day in a calendar month.
type DayCount struct {
	Day   int `json:"day"`
	Count int `json:"count"`
}

// MonthData is the stable single-month day-count response.
type MonthData struct {
	Month string     `json:"month"`
	Days  []DayCount `json:"days"`
}

// FromSearch maps the application page without exposing internal Entry fields.
func FromSearch(page app.SearchPage) SearchData {
	items := make([]Hit, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, Hit{Post: postdto.FromPost(item.Post), Snippet: item.Snippet})
	}
	years := make([]YearCount, 0, len(page.YearCounts))
	for _, item := range page.YearCounts {
		years = append(years, YearCount{Year: item.Year, Count: item.Count})
	}
	return SearchData{Items: items, YearCounts: years}
}

// FromMonths maps application calendar aggregates to the wire contract.
func FromMonths(value app.ArchiveMonths) MonthsData {
	items := make([]MonthCount, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, MonthCount{Year: item.Year, Month: item.Month, Count: item.Count})
	}
	return MonthsData{Items: items}
}

// FromMonth maps one application calendar month to the wire contract.
func FromMonth(value app.ArchiveMonth) MonthData {
	days := make([]DayCount, 0, len(value.Days))
	for _, item := range value.Days {
		days = append(days, DayCount{Day: item.Day, Count: item.Count})
	}
	return MonthData{Month: value.Month, Days: days}
}

// NextCursor maps an exhausted cursor to JSON null.
func NextCursor(page app.SearchPage) *string {
	if page.NextCursor == "" {
		return nil
	}
	value := page.NextCursor
	return &value
}

// ValidateSearch rejects a superficially successful non-canonical API DTO.
func ValidateSearch(value SearchData) error {
	if value.Items == nil || value.YearCounts == nil {
		return errors.New("search items and year_counts must be arrays")
	}
	for _, item := range value.Items {
		if err := postdto.ValidatePost(item.Post); err != nil {
			return err
		}
		if strings.TrimSpace(item.Snippet) == "" {
			return errors.New("search snippet must contain visible text")
		}
	}
	for index, item := range value.YearCounts {
		if item.Year < 1 || item.Count < 1 || (index > 0 && value.YearCounts[index-1].Year <= item.Year) {
			return errors.New("search year_counts are invalid or unordered")
		}
	}
	return nil
}

// ValidateMonths enforces deterministic reverse calendar order.
func ValidateMonths(value MonthsData) error {
	if value.Items == nil {
		return errors.New("archive month items must be an array")
	}
	for index, item := range value.Items {
		if item.Year < 1 || item.Month < 1 || item.Month > 12 || item.Count < 1 {
			return errors.New("archive month count is invalid")
		}
		if index > 0 {
			previous := value.Items[index-1]
			if previous.Year < item.Year || (previous.Year == item.Year && previous.Month <= item.Month) {
				return errors.New("archive months are unordered")
			}
		}
	}
	return nil
}

// ValidateMonth enforces deterministic day order and bounded values.
func ValidateMonth(value MonthData) error {
	if len(value.Month) != len("2006-01") || value.Days == nil {
		return errors.New("archive month data is incomplete")
	}
	if !sort.SliceIsSorted(value.Days, func(i, j int) bool { return value.Days[i].Day < value.Days[j].Day }) {
		return errors.New("archive days are unordered")
	}
	for _, item := range value.Days {
		if item.Day < 1 || item.Day > 31 || item.Count < 1 {
			return errors.New("archive day count is invalid")
		}
	}
	return nil
}
