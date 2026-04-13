package matcher

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"BNLS CHKN BRST", "bnls chkn brst"},
		{"Hello, World!", "hello world"},
		{"  extra   spaces  ", "extra spaces"},
		{"Price: $3.49/lb", "price 3 49 lb"},
		{"ALL-PURPOSE FLOUR", "all purpose flour"},
		{"", ""},
		{"simple", "simple"},
		{"MIX & MATCH", "mix match"},
		{"item\t\twith\ttabs", "item with tabs"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Normalize(tt.input)
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
