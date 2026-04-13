package matcher

import "testing"

func TestEvaluateCondition(t *testing.T) {
	tests := []struct {
		name         string
		normalized   string
		conditionVal string
		op           string
		want         bool
	}{
		{"exact match", "chicken breast", "chicken breast", "exact", true},
		{"exact no match", "chicken breast", "chicken brst", "exact", false},
		{"contains match", "boneless chicken breast", "chicken", "contains", true},
		{"contains no match", "boneless chicken breast", "pork", "contains", false},
		{"starts_with match", "chicken breast boneless", "chicken", "starts_with", true},
		{"starts_with no match", "boneless chicken breast", "chicken", "starts_with", false},
		{"matches regex", "chicken breast 3lb", `chicken.*\dlb`, "matches", true},
		{"matches regex no match", "pork chops", `chicken.*\dlb`, "matches", false},
		{"invalid op", "chicken", "chicken", "unknown", false},
		{"matches invalid regex", "test", `[invalid`, "matches", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateCondition(tt.normalized, tt.conditionVal, tt.op)
			if got != tt.want {
				t.Errorf("evaluateCondition(%q, %q, %q) = %v, want %v",
					tt.normalized, tt.conditionVal, tt.op, got, tt.want)
			}
		})
	}
}
