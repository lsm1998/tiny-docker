package stringutil

import "fmt"

func ShortImageID(id string) string {
	if id == "" {
		return "<none>"
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func FormatBytes(size int64) string {
	if size <= 0 {
		return "0B"
	}
	units := []string{"B", "kB", "MB", "GB", "TB"}
	value := float64(size)
	unit := units[0]
	for i := 1; i < len(units) && value >= 1024; i++ {
		value /= 1024
		unit = units[i]
	}
	if value >= 10 || unit == "B" {
		return fmt.Sprintf("%.0f%s", value, unit)
	}
	return fmt.Sprintf("%.1f%s", value, unit)
}
