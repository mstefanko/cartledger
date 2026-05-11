package adapters

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mstefanko/cartledger/internal/enrichment"
)

const KrogerSource = "kroger"

var (
	upcRE                  = regexp.MustCompile(`(?i)\b(?:upc|gtin)\s*:?\s*([0-9]{8,14})\b`)
	brandRE                = regexp.MustCompile(`(?i)\b(?:brand|manufacturer)\s*:?\s*([A-Za-z0-9][A-Za-z0-9 '&.\-]{1,80})`)
	sizeLabelRE            = regexp.MustCompile(`(?i)\b(?:net wt|net weight|size|package size)\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z][a-zA-Z _./-]{0,16})\b`)
	inlineSizeRE           = regexp.MustCompile(`(?i)\b([0-9]+(?:\.[0-9]+)?)\s*(fl\s*oz|fluid\s*ounces?|oz|ounces?|lb|pounds?|g|grams?|kg|kilograms?|ml|milliliters?|l|liters?|ct|count|ea|each)\b`)
	servingSizeRE          = regexp.MustCompile(`(?i)\bserving size\s*:?\s*([0-9]+(?:\.[0-9]+)?)?\s*([a-zA-Z][a-zA-Z _./-]*)(?:\s*\(([^)]*)\))?`)
	servingsPerContainerRE = regexp.MustCompile(`(?i)\bservings per container\s*:?\s*(?:about\s*)?([0-9]+(?:\.[0-9]+)?)`)
	caloriesRE             = regexp.MustCompile(`(?i)\bcalories\s*:?\s*([0-9]+(?:\.[0-9]+)?)`)
	totalFatRE             = regexp.MustCompile(`(?i)\btotal fat\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	saturatedFatRE         = regexp.MustCompile(`(?i)\bsaturated fat\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	transFatRE             = regexp.MustCompile(`(?i)\btrans fat\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	cholesterolRE          = regexp.MustCompile(`(?i)\bcholesterol\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*mg\b`)
	sodiumRE               = regexp.MustCompile(`(?i)\bsodium\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*mg\b`)
	carbsRE                = regexp.MustCompile(`(?i)\btotal carbohydrate\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	fiberRE                = regexp.MustCompile(`(?i)\bdietary fiber\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	totalSugarsRE          = regexp.MustCompile(`(?i)\btotal sugars\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	addedSugarsRE          = regexp.MustCompile(`(?i)\badded sugars\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
	proteinRE              = regexp.MustCompile(`(?i)\bprotein\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*g\b`)
)

func Matches(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "kroger.com" || strings.HasSuffix(host, ".kroger.com")
}

func Parse(rawURL, text string) []enrichment.Suggestion {
	text = enrichment.NormalizeText(text)
	if text == "" {
		return nil
	}

	var out []enrichment.Suggestion
	add := func(field, value, evidence string, confidence float64) {
		value = strings.TrimSpace(value)
		if value == "" || hasField(out, field) {
			return
		}
		out = append(out, enrichment.NewSuggestion(KrogerSource, rawURL, field, value, trimEvidence(evidence), confidence))
	}

	if name := productName(text); name != "" {
		add("name", name, name, 0.78)
	}
	if m := brandRE.FindStringSubmatch(text); len(m) == 2 {
		add("brand", cleanupLabel(m[1]), m[0], 0.72)
	}
	if m := upcRE.FindStringSubmatch(text); len(m) == 2 {
		add("upc", m[1], m[0], 0.92)
	}
	if m := sizeLabelRE.FindStringSubmatch(text); len(m) == 3 {
		add("pack_quantity", m[1], m[0], 0.82)
		add("pack_unit", canonicalUnit(m[2]), m[0], 0.82)
	} else if name := productName(text); name != "" {
		if m := inlineSizeRE.FindStringSubmatch(name); len(m) == 3 {
			add("pack_quantity", m[1], m[0], 0.7)
			add("pack_unit", canonicalUnit(m[2]), m[0], 0.7)
		}
	}

	if m := servingSizeRE.FindStringSubmatch(text); len(m) >= 3 {
		evidence := m[0]
		if m[1] != "" {
			add("serving_quantity", m[1], evidence, 0.76)
			add("serving_unit", canonicalUnit(m[2]), evidence, 0.76)
		} else {
			add("serving_label", cleanupLabel(m[2]), evidence, 0.72)
		}
		if len(m) >= 4 && strings.TrimSpace(m[3]) != "" {
			add("serving_label", cleanupLabel(m[3]), evidence, 0.72)
		}
	}
	if m := servingsPerContainerRE.FindStringSubmatch(text); len(m) == 2 {
		add("servings_per_container", m[1], m[0], 0.78)
	}

	addNumber := func(field string, re *regexp.Regexp) {
		if m := re.FindStringSubmatch(text); len(m) == 2 {
			add(field, m[1], m[0], 0.76)
		}
	}
	addNumber("calories", caloriesRE)
	addNumber("total_fat_g", totalFatRE)
	addNumber("saturated_fat_g", saturatedFatRE)
	addNumber("trans_fat_g", transFatRE)
	addNumber("cholesterol_mg", cholesterolRE)
	addNumber("sodium_mg", sodiumRE)
	addNumber("total_carbohydrate_g", carbsRE)
	addNumber("dietary_fiber_g", fiberRE)
	addNumber("total_sugars_g", totalSugarsRE)
	addNumber("added_sugars_g", addedSugarsRE)
	addNumber("protein_g", proteinRE)

	if ingredients := sectionAfter(text, []string{"Ingredients"}, []string{"Contains", "Allergen", "Nutrition", "Directions", "Warnings"}); ingredients != "" {
		add("ingredients", ingredients, "Ingredients: "+ingredients, 0.78)
	}
	if allergens := sectionAfter(text, []string{"Contains", "Allergen Info", "Allergens"}, []string{"Nutrition", "Directions", "Warnings"}); allergens != "" {
		add("allergens", allergens, "Allergens: "+allergens, 0.72)
	}
	return out
}

func productName(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		clean := cleanupLabel(line)
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		if skipProductNameLine(lower) {
			continue
		}
		if strings.Contains(lower, "kroger") && (strings.Contains(lower, " - ") || strings.Contains(lower, "|")) {
			clean = strings.TrimSpace(strings.Split(strings.Split(clean, " - ")[0], "|")[0])
			lower = strings.ToLower(clean)
			if skipProductNameLine(lower) {
				continue
			}
		}
		if len(clean) >= 4 {
			return clean
		}
	}
	return ""
}

func skipProductNameLine(lower string) bool {
	switch lower {
	case "kroger", "shop", "departments", "sign in", "shopping cart", "cart", "menu":
		return true
	}
	return strings.Contains(lower, "sign in") ||
		strings.Contains(lower, "shopping cart") ||
		strings.Contains(lower, "pickup at") ||
		strings.Contains(lower, "delivery to")
}

func sectionAfter(text string, starts []string, stops []string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, start := range starts {
			if strings.EqualFold(trimmed, start) || strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(start)+":") {
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, start))
				value = strings.TrimPrefix(value, ":")
				value = strings.TrimSpace(value)
				if value != "" {
					return trimEvidence(value)
				}
				var parts []string
				for _, next := range lines[i+1:] {
					next = strings.TrimSpace(next)
					if next == "" {
						continue
					}
					if isStop(next, stops) {
						break
					}
					parts = append(parts, next)
					if len(strings.Join(parts, " ")) > 600 {
						break
					}
				}
				return trimEvidence(strings.Join(parts, " "))
			}
		}
	}
	return ""
}

func isStop(line string, stops []string) bool {
	lower := strings.ToLower(line)
	for _, stop := range stops {
		if strings.HasPrefix(lower, strings.ToLower(stop)) {
			return true
		}
	}
	return false
}

func hasField(items []enrichment.Suggestion, field string) bool {
	for _, item := range items {
		if item.Field == field {
			return true
		}
	}
	return false
}

func cleanupLabel(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return strings.Trim(value, " :-|")
}

func trimEvidence(value string) string {
	value = cleanupLabel(value)
	if len(value) > 280 {
		return strings.TrimSpace(value[:280]) + "..."
	}
	return value
}

func canonicalUnit(unit string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(unit), " "))
	normalized = strings.Trim(normalized, ".")
	switch normalized {
	case "ounces", "ounce":
		return "oz"
	case "fl oz", "fluid ounce", "fluid ounces":
		return "fl_oz"
	case "pounds", "pound", "lbs":
		return "lb"
	case "grams", "gram":
		return "g"
	case "kilograms", "kilogram":
		return "kg"
	case "milliliters", "milliliter":
		return "ml"
	case "liters", "liter", "litres", "litre":
		return "l"
	case "count", "ct", "ea":
		return "each"
	}
	if _, err := strconv.ParseFloat(normalized, 64); err == nil {
		return ""
	}
	return normalized
}
