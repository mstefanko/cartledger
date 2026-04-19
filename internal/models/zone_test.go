package models

import "testing"

func TestCategoryToZone(t *testing.T) {
	tests := []struct {
		name     string
		category string
		want     StorageZone
	}{
		// All 11 canonical LLM categories
		{"Meat is cold", "Meat", ZoneCold},
		{"Produce is produce", "Produce", ZoneProduce},
		{"Dairy is cold", "Dairy", ZoneCold},
		{"Bakery is other", "Bakery", ZoneOther},
		{"Frozen is frozen", "Frozen", ZoneFrozen},
		{"Pantry is other", "Pantry", ZoneOther},
		{"Snacks is other", "Snacks", ZoneOther},
		{"Beverages is other", "Beverages", ZoneOther},
		{"Household is other", "Household", ZoneOther},
		{"Health is other", "Health", ZoneOther},
		{"Other is other", "Other", ZoneOther},

		// Empty / unknown
		{"empty string is other", "", ZoneOther},
		{"unknown is other", "unknown", ZoneOther},
		{"random free-text is other", "Pet Food", ZoneOther},

		// Mixed case
		{"lowercase produce", "produce", ZoneProduce},
		{"uppercase MEAT", "MEAT", ZoneCold},
		{"mixed case dAiRy", "dAiRy", ZoneCold},
		{"lowercase frozen", "frozen", ZoneFrozen},
		{"uppercase OTHER", "OTHER", ZoneOther},

		// Whitespace
		{"produce with whitespace", "  Produce  ", ZoneProduce},
		{"meat with leading space", " Meat", ZoneCold},
		{"frozen with trailing space", "Frozen ", ZoneFrozen},
		{"dairy with tabs", "\tDairy\t", ZoneCold},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CategoryToZone(tt.category)
			if got != tt.want {
				t.Errorf("CategoryToZone(%q) = %q, want %q", tt.category, got, tt.want)
			}
		})
	}
}

func TestZoneSortOrder(t *testing.T) {
	tests := []struct {
		name string
		zone StorageZone
		want int
	}{
		{"produce is 0", ZoneProduce, 0},
		{"cold is 1", ZoneCold, 1},
		{"frozen is 2", ZoneFrozen, 2},
		{"other is 3", ZoneOther, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ZoneSortOrder(tt.zone)
			if got != tt.want {
				t.Errorf("ZoneSortOrder(%q) = %d, want %d", tt.zone, got, tt.want)
			}
		})
	}
}
