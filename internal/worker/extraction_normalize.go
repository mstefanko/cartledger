package worker

import (
	"math"
	"regexp"
	"strings"

	"github.com/mstefanko/cartledger/internal/llm"
)

var couponItemRefPattern = regexp.MustCompile(`/(\d{4,})\b`)
var paymentMaskedLast4Pattern = regexp.MustCompile(`(?i)(?:[x*.#][\s-]*){2,}([0-9]{4})\b`)
var paymentBareLast4Pattern = regexp.MustCompile(`(?i)\b(?:acct|account|card|pan)\b[^0-9]{0,16}([0-9]{4})\b`)
var paymentBrandPattern = regexp.MustCompile(`(?i)\b(american express|amex|master\s*card|mastercard|visa|discover|debit|ebt|cash|check)\b`)

// NormalizeExtractedPayment keeps tender metadata conservative. The LLM is
// good at seeing the card brand, but it sometimes copies nearby auth/sequence
// numbers as the last 4. When payment_card_raw is present, only trust a last 4
// that can be parsed from a masked account line in that raw payment section.
func NormalizeExtractedPayment(extraction *llm.ReceiptExtraction) {
	if extraction == nil {
		return
	}
	extraction.PaymentCardType = normalizePaymentCardType(extraction.PaymentCardType, extraction.PaymentCardRaw)
	extraction.PaymentCardLast4 = normalizePaymentCardLast4(extraction.PaymentCardType, extraction.PaymentCardLast4, extraction.PaymentCardRaw)
}

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

func normalizePaymentCardType(cardType, raw *string) *string {
	modelType := ""
	if cardType != nil {
		modelType = canonicalPaymentCardType(*cardType)
	}
	rawType := paymentCardTypeFromRaw(raw)

	if rawType != "" && isCardNetwork(rawType) && (modelType == "" || !isCardNetwork(modelType)) {
		return stringPtr(rawType)
	}
	if modelType != "" {
		return stringPtr(modelType)
	}
	if rawType != "" {
		return stringPtr(rawType)
	}
	return nil
}

func normalizePaymentCardLast4(cardType, modelLast4, raw *string) *string {
	if cardType == nil || paymentTypeHasNoLast4(*cardType) {
		return nil
	}

	if raw != nil && strings.TrimSpace(*raw) != "" {
		if last4 := paymentLast4FromRaw(*raw); last4 != "" {
			return stringPtr(last4)
		}
		return nil
	}

	if modelLast4 == nil {
		return nil
	}
	if last4, ok := fourDigitsOnly(*modelLast4); ok {
		return stringPtr(last4)
	}
	return nil
}

func paymentCardTypeFromRaw(raw *string) string {
	if raw == nil {
		return ""
	}
	matches := paymentBrandPattern.FindAllString(*raw, -1)
	for _, preferred := range []string{"Amex", "Visa", "Mastercard", "Discover", "EBT", "Debit", "Cash", "Check"} {
		for _, match := range matches {
			if canonicalPaymentCardType(match) == preferred {
				return preferred
			}
		}
	}
	return ""
}

func canonicalPaymentCardType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch {
	case strings.Contains(normalized, "american express") || strings.Contains(normalized, "amex"):
		return "Amex"
	case strings.Contains(normalized, "mastercard") || strings.Contains(normalized, "master card"):
		return "Mastercard"
	case strings.Contains(normalized, "visa"):
		return "Visa"
	case strings.Contains(normalized, "discover"):
		return "Discover"
	case strings.Contains(normalized, "ebt"):
		return "EBT"
	case strings.Contains(normalized, "debit"):
		return "Debit"
	case strings.Contains(normalized, "cash"):
		return "Cash"
	case strings.Contains(normalized, "check"):
		return "Check"
	default:
		return ""
	}
}

func isCardNetwork(cardType string) bool {
	switch cardType {
	case "Visa", "Mastercard", "Amex", "Discover":
		return true
	default:
		return false
	}
}

func paymentTypeHasNoLast4(cardType string) bool {
	switch cardType {
	case "Cash", "Check":
		return true
	default:
		return false
	}
}

func paymentLast4FromRaw(raw string) string {
	lines := paymentRawLines(raw)
	if len(lines) == 0 {
		return ""
	}

	brandLines := make([]int, 0)
	for i, line := range lines {
		if paymentBrandPattern.MatchString(line) {
			brandLines = append(brandLines, i)
		}
	}

	bestLast4 := ""
	bestScore := -1
	for i, line := range lines {
		for _, match := range paymentMaskedLast4Pattern.FindAllStringSubmatch(line, -1) {
			if len(match) < 2 {
				continue
			}
			score := paymentLast4CandidateScore(line, i, brandLines)
			if score > bestScore {
				bestScore = score
				bestLast4 = match[1]
			}
		}
	}
	if bestLast4 != "" {
		return bestLast4
	}

	for i, line := range lines {
		if !paymentLineHasAccountLabel(line) && nearestPaymentBrandDistance(i, brandLines) > 1 {
			continue
		}
		match := paymentBareLast4Pattern.FindStringSubmatch(line)
		if len(match) >= 2 {
			return match[1]
		}
	}
	return ""
}

func paymentRawLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	parts := strings.Split(raw, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		line := strings.TrimSpace(part)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func paymentLast4CandidateScore(line string, index int, brandLines []int) int {
	score := 10
	if paymentBrandPattern.MatchString(line) {
		score += 8
	}
	if distance := nearestPaymentBrandDistance(index, brandLines); distance >= 0 {
		score += maxInt(0, 6-distance)
	}
	if paymentLineHasAccountLabel(line) {
		score += 3
	}
	if paymentLineHasExcludedNumberLabel(line) {
		score -= 6
	}
	return score
}

func nearestPaymentBrandDistance(index int, brandLines []int) int {
	if len(brandLines) == 0 {
		return -1
	}
	best := -1
	for _, brandLine := range brandLines {
		distance := index - brandLine
		if distance < 0 {
			distance = -distance
		}
		if best == -1 || distance < best {
			best = distance
		}
	}
	return best
}

func paymentLineHasAccountLabel(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "acct") ||
		strings.Contains(lower, "account") ||
		strings.Contains(lower, "card") ||
		strings.Contains(lower, "pan")
}

func paymentLineHasExcludedNumberLabel(line string) bool {
	lower := strings.ToLower(line)
	excluded := []string{"aid", "seq", "app", "auth", "approval", "approved", "tran", "trans", "transaction", "ref", "member", "merchant", "store", "phone", "zip", "subtotal", "total", "tax"}
	for _, term := range excluded {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func fourDigitsOnly(value string) (string, bool) {
	var digits strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	out := digits.String()
	return out, len(out) == 4
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

func stringPtr(v string) *string {
	return &v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
