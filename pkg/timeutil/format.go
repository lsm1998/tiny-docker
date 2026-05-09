package timeutil

import (
	"fmt"
	"time"
)

func FormatRelativeTime(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	delta := max(time.Since(parsed), 0)

	switch {
	case delta < time.Minute:
		return "less than a minute ago"
	case delta < time.Hour:
		minutes := int(delta / time.Minute)
		return pluralizeAgo(minutes, "minute")
	case delta < 24*time.Hour:
		hours := int(delta / time.Hour)
		return pluralizeAgo(hours, "hour")
	case delta < 30*24*time.Hour:
		days := int(delta / (24 * time.Hour))
		return pluralizeAgo(days, "day")
	case delta < 365*24*time.Hour:
		months := max(int(delta/(30*24*time.Hour)), 1)
		return pluralizeAgo(months, "month")
	default:
		years := max(int(delta/(365*24*time.Hour)), 1)
		return pluralizeAgo(years, "year")
	}
}

func pluralizeAgo(value int, unit string) string {
	if value == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", value, unit)
}
