package upc

import "github.com/mstefanko/cartledger/internal/identifiers"

func Normalize(value string) (string, error) {
	return identifiers.NormalizeGTIN(value)
}

func NormalizePointer(value *string) *string {
	if value == nil {
		return nil
	}
	normalized, err := Normalize(*value)
	if err != nil || normalized == "" {
		return nil
	}
	return &normalized
}
