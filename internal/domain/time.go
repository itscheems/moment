package domain

import (
	"time"
)

const dayLayout = "2006-01-02"

// NormalizeTime stores domain instants at UTC millisecond precision.
func NormalizeTime(value time.Time) time.Time {
	return time.UnixMilli(value.UTC().UnixMilli()).UTC()
}

// TimeFromMillis maps the SQLite representation into a UTC domain instant.
func TimeFromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

// TimeToMillis maps a domain instant into the SQLite representation.
func TimeToMillis(value time.Time) int64 {
	return value.UTC().UnixMilli()
}

// FormatTime maps a domain instant into the API representation.
func FormatTime(value time.Time) string {
	return NormalizeTime(value).Format(time.RFC3339Nano)
}

// DayRange converts one site-calendar date into a half-open UTC range. Adding a
// calendar day in the site location intentionally respects 23/25-hour DST days.
func DayRange(date, timezone string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, NewError(CodeInvalidArgument, "timezone must be a valid IANA name", map[string]any{"field": "timezone"})
	}
	startLocal, err := time.ParseInLocation(dayLayout, date, location)
	if err != nil || startLocal.Format(dayLayout) != date {
		return time.Time{}, time.Time{}, NewError(CodeInvalidArgument, "date must use YYYY-MM-DD", map[string]any{"field": "date"})
	}
	endLocal := startLocal.AddDate(0, 0, 1)
	return startLocal.UTC(), endLocal.UTC(), nil
}
