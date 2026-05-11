package llm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type receiptExtractionJSON struct {
	StoreName        string          `json:"store_name"`
	StoreAddress     *string         `json:"store_address"`
	StoreCity        *string         `json:"store_city"`
	StoreState       *string         `json:"store_state"`
	StoreZip         *string         `json:"store_zip"`
	StoreNumber      *string         `json:"store_number"`
	Date             string          `json:"date"`
	PaymentCardType  *string         `json:"payment_card_type"`
	PaymentCardLast4 *string         `json:"payment_card_last4"`
	PaymentCardRaw   *string         `json:"payment_card_raw"`
	Time             *string         `json:"time"`
	ItemsSoldCount   *int            `json:"items_sold_count"`
	Items            []ExtractedItem `json:"items"`
	Subtotal         flexibleFloat   `json:"subtotal"`
	Tax              flexibleFloat   `json:"tax"`
	Total            flexibleFloat   `json:"total"`
	Confidence       flexibleFloat   `json:"confidence"`
}

func (r *ReceiptExtraction) UnmarshalJSON(data []byte) error {
	var aux receiptExtractionJSON
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*r = ReceiptExtraction{
		StoreName:        aux.StoreName,
		StoreAddress:     aux.StoreAddress,
		StoreCity:        aux.StoreCity,
		StoreState:       aux.StoreState,
		StoreZip:         aux.StoreZip,
		StoreNumber:      aux.StoreNumber,
		Date:             aux.Date,
		PaymentCardType:  aux.PaymentCardType,
		PaymentCardLast4: aux.PaymentCardLast4,
		PaymentCardRaw:   aux.PaymentCardRaw,
		Time:             aux.Time,
		ItemsSoldCount:   aux.ItemsSoldCount,
		Items:            aux.Items,
		Subtotal:         float64(aux.Subtotal),
		Tax:              float64(aux.Tax),
		Total:            float64(aux.Total),
		Confidence:       float64(aux.Confidence),
	}
	return nil
}

type extractedItemJSON struct {
	RawName            string        `json:"raw_name"`
	StoreItemCode      *string       `json:"store_item_code"`
	ReceiptDescription *string       `json:"receipt_description"`
	SuggestedName      string        `json:"suggested_name"`
	SuggestedCategory  string        `json:"suggested_category"`
	SuggestedBrand     string        `json:"suggested_brand"`
	SuggestedTags      string        `json:"suggested_tags"`
	Quantity           flexibleFloat `json:"quantity"`
	Unit               *string       `json:"unit"`
	UnitPrice          nullableFloat `json:"unit_price"`
	TotalPrice         flexibleFloat `json:"total_price"`
	RegularPrice       nullableFloat `json:"regular_price"`
	DiscountAmount     nullableFloat `json:"discount_amount"`
	CountContribution  flexibleFloat `json:"count_contribution"`
	LineNumber         int           `json:"line_number"`
	Confidence         flexibleFloat `json:"confidence"`
}

func (i *ExtractedItem) UnmarshalJSON(data []byte) error {
	var aux extractedItemJSON
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*i = ExtractedItem{
		RawName:            aux.RawName,
		StoreItemCode:      aux.StoreItemCode,
		ReceiptDescription: aux.ReceiptDescription,
		SuggestedName:      aux.SuggestedName,
		SuggestedCategory:  aux.SuggestedCategory,
		SuggestedBrand:     aux.SuggestedBrand,
		SuggestedTags:      aux.SuggestedTags,
		Quantity:           float64(aux.Quantity),
		Unit:               aux.Unit,
		UnitPrice:          aux.UnitPrice.ptr(),
		TotalPrice:         float64(aux.TotalPrice),
		RegularPrice:       aux.RegularPrice.ptr(),
		DiscountAmount:     aux.DiscountAmount.ptr(),
		CountContribution:  float64(aux.CountContribution),
		LineNumber:         aux.LineNumber,
		Confidence:         float64(aux.Confidence),
	}
	return nil
}

type flexibleFloat float64

func (f *flexibleFloat) UnmarshalJSON(data []byte) error {
	v, _, err := parseJSONFloat(data)
	if err != nil {
		return err
	}
	*f = flexibleFloat(v)
	return nil
}

type nullableFloat struct {
	value float64
	valid bool
}

func (n *nullableFloat) UnmarshalJSON(data []byte) error {
	v, valid, err := parseJSONFloat(data)
	if err != nil {
		return err
	}
	n.value = v
	n.valid = valid
	return nil
}

func (n nullableFloat) ptr() *float64 {
	if !n.valid {
		return nil
	}
	v := n.value
	return &v
}

func parseJSONFloat(data []byte) (float64, bool, error) {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return 0, false, nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return 0, false, err
		}
		s = normalizeNumericString(s)
		if s == "" || strings.EqualFold(s, "null") {
			return 0, false, nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parse numeric string %q: %w", s, err)
		}
		return v, true, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse numeric value %q: %w", raw, err)
	}
	return v, true, nil
}

func normalizeNumericString(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.HasSuffix(s, "-") && len(s) > 1 {
		s = "-" + strings.TrimSuffix(s, "-")
	}
	return s
}
