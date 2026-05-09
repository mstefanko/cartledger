package worker

import (
	"math"
	"regexp"
	"strings"

	"github.com/mstefanko/cartledger/internal/llm"
)

var couponItemRefPattern = regexp.MustCompile(`/(\d{4,})\b`)

func NormalizeExtractedItems(items []llm.ExtractedItem) []llm.ExtractedItem {
	normalized := make([]llm.ExtractedItem, 0, len(items))
	for _, item := range items {
		if isDiscountAdjustment(item) {
			applyDiscountAdjustment(normalized, item)
			continue
		}

		if item.CountContribution <= 0 {
			item.CountContribution = defaultCountContribution(item)
		}

		if len(normalized) > 0 && canMergeCountableDuplicate(normalized[len(normalized)-1], item) {
			mergeCountableDuplicate(&normalized[len(normalized)-1], item)
			continue
		}

		normalized = append(normalized, item)
	}
	return normalized
}

func isDiscountAdjustment(item llm.ExtractedItem) bool {
	if item.TotalPrice < 0 {
		return true
	}
	text := strings.ToLower(item.RawName + " " + item.SuggestedName + " " + item.SuggestedTags)
	return item.TotalPrice <= 0 && (strings.Contains(text, "coupon") ||
		strings.Contains(text, "discount") ||
		strings.Contains(text, "savings") ||
		strings.Contains(text, "member price"))
}

func applyDiscountAdjustment(items []llm.ExtractedItem, adjustment llm.ExtractedItem) {
	if len(items) == 0 {
		return
	}

	discount := math.Abs(adjustment.TotalPrice)
	if discount == 0 {
		return
	}

	targetIndex := len(items) - 1
	if ref := couponItemReference(adjustment.RawName); ref != "" {
		for i := len(items) - 1; i >= 0; i-- {
			if itemContainsReference(items[i].RawName, ref) {
				targetIndex = i
				break
			}
		}
	}

	target := &items[targetIndex]
	regular := target.TotalPrice
	if target.RegularPrice != nil {
		regular = *target.RegularPrice
	} else {
		target.RegularPrice = floatPtr(regular)
	}

	existingDiscount := 0.0
	if target.DiscountAmount != nil {
		existingDiscount = *target.DiscountAmount
	}
	totalDiscount := existingDiscount + discount
	target.DiscountAmount = floatPtr(totalDiscount)
	target.TotalPrice = regular - totalDiscount
	if target.TotalPrice < 0 {
		target.TotalPrice = 0
	}
	if target.UnitPrice != nil && target.Quantity > 0 {
		target.UnitPrice = floatPtr(target.TotalPrice / target.Quantity)
	}
	if adjustment.Confidence > 0 && target.Confidence > adjustment.Confidence {
		target.Confidence = adjustment.Confidence
	}
}

func couponItemReference(rawName string) string {
	match := couponItemRefPattern.FindStringSubmatch(rawName)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func itemContainsReference(rawName, ref string) bool {
	for _, field := range strings.FieldsFunc(rawName, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		if field == ref {
			return true
		}
	}
	return false
}

func defaultCountContribution(item llm.ExtractedItem) float64 {
	if isCountableUnit(item.Unit) && isWholeNumber(item.Quantity) && item.Quantity > 0 {
		return item.Quantity
	}
	return 1
}

func canMergeCountableDuplicate(a, b llm.ExtractedItem) bool {
	if normalizeReceiptName(a.RawName) == "" || normalizeReceiptName(a.RawName) != normalizeReceiptName(b.RawName) {
		return false
	}
	if !sameOptionalString(a.Unit, b.Unit) || !isCountableUnit(a.Unit) || !isCountableUnit(b.Unit) {
		return false
	}
	if !isWholeNumber(a.Quantity) || !isWholeNumber(b.Quantity) {
		return false
	}
	return nearlyEqual(a.TotalPrice, b.TotalPrice) &&
		sameOptionalFloat(a.RegularPrice, b.RegularPrice) &&
		sameOptionalFloat(a.DiscountAmount, b.DiscountAmount)
}

func mergeCountableDuplicate(target *llm.ExtractedItem, item llm.ExtractedItem) {
	target.Quantity += item.Quantity
	target.TotalPrice += item.TotalPrice
	target.CountContribution += item.CountContribution
	if target.RegularPrice != nil && item.RegularPrice != nil {
		target.RegularPrice = floatPtr(*target.RegularPrice + *item.RegularPrice)
	}
	if target.DiscountAmount != nil && item.DiscountAmount != nil {
		target.DiscountAmount = floatPtr(*target.DiscountAmount + *item.DiscountAmount)
	}
	if target.UnitPrice != nil && item.UnitPrice != nil && !nearlyEqual(*target.UnitPrice, *item.UnitPrice) {
		target.UnitPrice = floatPtr(target.TotalPrice / target.Quantity)
	}
	if item.Confidence > 0 && item.Confidence < target.Confidence {
		target.Confidence = item.Confidence
	}
}

func normalizeReceiptName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

func isCountableUnit(unit *string) bool {
	if unit == nil || strings.TrimSpace(*unit) == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(*unit)) {
	case "each", "ea", "pack", "ct", "count", "gal", "qt", "pt":
		return true
	default:
		return false
	}
}

func sameOptionalString(a, b *string) bool {
	av, bv := "", ""
	if a != nil {
		av = strings.ToLower(strings.TrimSpace(*a))
	}
	if b != nil {
		bv = strings.ToLower(strings.TrimSpace(*b))
	}
	return av == bv
}

func sameOptionalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return nearlyEqual(*a, *b)
}

func isWholeNumber(v float64) bool {
	return nearlyEqual(v, math.Round(v))
}

func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.000001
}

func floatPtr(v float64) *float64 {
	return &v
}
