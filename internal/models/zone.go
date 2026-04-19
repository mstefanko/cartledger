package models

import "strings"

type StorageZone string

const (
	ZoneProduce StorageZone = "produce"
	ZoneCold    StorageZone = "cold"
	ZoneFrozen  StorageZone = "frozen"
	ZoneOther   StorageZone = "other"
)

// CategoryToZone maps the 11 LLM categories (and any free-text product.category)
// to the 4 shopping zones. Case-insensitive. Unknown / empty → ZoneOther.
func CategoryToZone(category string) StorageZone {
	c := strings.TrimSpace(category)
	switch {
	case strings.EqualFold(c, "Produce"):
		return ZoneProduce
	case strings.EqualFold(c, "Meat"), strings.EqualFold(c, "Dairy"):
		return ZoneCold
	case strings.EqualFold(c, "Frozen"):
		return ZoneFrozen
	default:
		return ZoneOther
	}
}

// ZoneSortOrder returns the stable display order (0=first).
func ZoneSortOrder(z StorageZone) int {
	switch z {
	case ZoneProduce:
		return 0
	case ZoneCold:
		return 1
	case ZoneFrozen:
		return 2
	default:
		return 3
	}
}
