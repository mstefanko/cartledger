package identifiers

import "testing"

func TestNormalizeGTIN(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"ean8", "9638-5074", "96385074", false},
		{"upca", "036000291452", "036000291452", false},
		{"ean13", "4006381333931", "4006381333931", false},
		{"gtin14", "10012345678902", "10012345678902", false},
		{"blank", " ", "", false},
		{"bad check digit", "036000291453", "", true},
		{"bad length", "123456", "", true},
		{"bad character", "03600029145X", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeGTIN(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeGTIN error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeGTIN error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeGTIN = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizePLU(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"4011", "4011", false},
		{"94011", "94011", false},
		{"84011", "", true},
		{"401", "", true},
	}
	for _, tt := range tests {
		got, authority, err := NormalizePLU(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("NormalizePLU(%q) error = nil, want error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizePLU(%q) error = %v", tt.raw, err)
		}
		if got != tt.want || authority != "ifps" {
			t.Fatalf("NormalizePLU(%q) = %q/%q, want %q/ifps", tt.raw, got, authority, tt.want)
		}
	}
}

func TestNormalizeExternalID(t *testing.T) {
	got, err := NormalizeExternalID("Open Food Facts", " ABC  123 ")
	if err != nil {
		t.Fatalf("NormalizeExternalID: %v", err)
	}
	if got != "abc 123" {
		t.Fatalf("NormalizeExternalID = %q, want abc 123", got)
	}
}
