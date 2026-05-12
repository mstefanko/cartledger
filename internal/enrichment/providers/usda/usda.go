package usda

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/enrichment/store"
	"github.com/mstefanko/cartledger/internal/httpsafe"
)

var (
	SearchAPIBase   = "https://api.nal.usda.gov/fdc/v1/foods/search"
	FoodDetailsBase = "https://fdc.nal.usda.gov/fdc-app.html#/food-details/"
)

type Provider struct {
	Client          *httpsafe.SafeHTTPClient
	SearchAPIBase   string
	FoodDetailsBase string
	Interval        time.Duration
}

func New(allowPrivate bool) *Provider {
	return &Provider{
		Client:          httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, allowPrivate),
		SearchAPIBase:   SearchAPIBase,
		FoodDetailsBase: FoodDetailsBase,
		Interval:        4 * time.Second,
	}
}

func (p *Provider) Name() string { return "usda_fdc" }

func (p *Provider) RateLimitKey() string {
	base := firstNonEmpty(p.SearchAPIBase, SearchAPIBase)
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "usda_fdc"
	}
	return u.Host
}

func (p *Provider) MinInterval() time.Duration { return p.Interval }

func (p *Provider) Lookup(ctx context.Context, input providers.LookupInput) ([]providers.Metadata, error) {
	if strings.TrimSpace(input.USDAAPIKey) == "" {
		return nil, nil
	}
	if input.UPC == nil || strings.TrimSpace(*input.UPC) == "" {
		return nil, nil
	}
	upc := strings.TrimSpace(*input.UPC)
	apiURL, _ := url.Parse(firstNonEmpty(p.SearchAPIBase, SearchAPIBase))
	query := apiURL.Query()
	query.Set("api_key", strings.TrimSpace(input.USDAAPIKey))
	query.Set("query", upc)
	query.Set("dataType", "Branded")
	query.Set("pageSize", "5")
	apiURL.RawQuery = query.Encode()

	defaultSourceURL := "https://fdc.nal.usda.gov/fdc-app.html#/food-search?query=" + url.QueryEscape(upc)
	client := p.Client
	if client == nil {
		client = httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, input.AllowPrivate)
	}
	result, err := client.Fetch(ctx, apiURL.String())
	if err != nil {
		return nil, err
	}
	if result.StatusCode == 429 {
		return nil, rateLimitError("usda_fdc rate limited")
	}

	sourceURL, payload, suggestions := payloadFromUSDA(upc, defaultSourceURL, firstNonEmpty(p.FoodDetailsBase, FoodDetailsBase), result.Body)
	hash := store.HashBytes(result.Body)
	confidence := sourceConfidenceForSuggestions(suggestions)
	sourceRecordID := upc
	if payload.SourceRecordID != nil && strings.TrimSpace(*payload.SourceRecordID) != "" {
		sourceRecordID = *payload.SourceRecordID
	}
	return []providers.Metadata{{
		Source:         "usda_fdc",
		SourceRecordID: &sourceRecordID,
		SourceURL:      sourceURL,
		LookupKey:      input.LookupKey,
		Confidence:     confidence,
		Payload:        payload,
		Suggestions:    suggestions,
		FetchedAt:      result.FetchedAt,
		HTTPStatus:     result.StatusCode,
		ContentHash:    &hash,
	}}, nil
}

func payloadFromUSDA(upc, defaultSourceURL, detailsBase string, body []byte) (string, enrichment.MetadataPayload, []enrichment.Suggestion) {
	var raw struct {
		Foods []struct {
			FdcID         int    `json:"fdcId"`
			Description   string `json:"description"`
			BrandOwner    string `json:"brandOwner"`
			GtinUPC       string `json:"gtinUpc"`
			Ingredients   string `json:"ingredients"`
			PackageWeight string `json:"packageWeight"`
			FoodNutrients []struct {
				NutrientName string  `json:"nutrientName"`
				UnitName     string  `json:"unitName"`
				Value        float64 `json:"value"`
			} `json:"foodNutrients"`
		} `json:"foods"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return defaultSourceURL, emptyPayload(upc, defaultSourceURL), nil
	}
	for _, food := range raw.Foods {
		if food.GtinUPC == "" || normalizeUSDAUPC(food.GtinUPC) != upc {
			continue
		}
		sourceURL := defaultSourceURL
		sourceRecordID := upc
		if food.FdcID > 0 {
			sourceURL = strings.TrimRight(detailsBase, "/") + "/" + strconv.Itoa(food.FdcID) + "/nutrients"
			sourceRecordID = strconv.Itoa(food.FdcID)
		}
		payload := enrichment.MetadataPayload{
			Version:        1,
			Source:         "usda_fdc",
			SourceRecordID: &sourceRecordID,
			SourceURL:      &sourceURL,
			Identifiers: []enrichment.PayloadIdentifier{{
				Type:      "gtin",
				Authority: stringPtr("usda_fdc"),
				Value:     upc,
			}},
		}
		var out []enrichment.Suggestion
		add := func(field, value, evidence string, confidence float64) {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, enrichment.NewSuggestion("usda_fdc", sourceURL, field, value, evidence, confidence))
			}
		}
		add("upc", upc, "USDA FoodData Central barcode match", 0.78)
		if name := titleDescription(food.Description); name != "" {
			payload.Name = &name
			add("name", name, food.Description, 0.62)
		}
		if brand := strings.TrimSpace(food.BrandOwner); brand != "" {
			payload.Brand = &brand
			add("brand", brand, brand, 0.62)
		}
		if ingredients := strings.TrimSpace(food.Ingredients); ingredients != "" {
			payload.Ingredients = &ingredients
			add("ingredients", ingredients, "Ingredients: "+ingredients, 0.62)
		}
		if pkg := strings.TrimSpace(food.PackageWeight); pkg != "" {
			payload.Evidence = append(payload.Evidence, enrichment.EvidencePayload{Field: "package", Text: pkg, URL: &sourceURL})
		}
		nutrients := &enrichment.NutrientPayload{}
		for _, n := range food.FoodNutrients {
			if field := usdaNutrientField(n.NutrientName, n.UnitName); field != "" && n.Value > 0 {
				setNutrient(nutrients, field, n.Value)
				add(field, strconv.FormatFloat(n.Value, 'f', -1, 64), n.NutrientName, 0.6)
			}
		}
		if hasNutrients(nutrients) {
			payload.Nutrients = nutrients
		}
		return sourceURL, payload, out
	}
	return defaultSourceURL, emptyPayload(upc, defaultSourceURL), nil
}

func emptyPayload(upc, sourceURL string) enrichment.MetadataPayload {
	return enrichment.MetadataPayload{
		Version:        1,
		Source:         "usda_fdc",
		SourceRecordID: &upc,
		SourceURL:      &sourceURL,
		Identifiers: []enrichment.PayloadIdentifier{{
			Type:      "gtin",
			Authority: stringPtr("usda_fdc"),
			Value:     upc,
		}},
	}
}

func usdaNutrientField(name, unit string) string {
	name = strings.ToLower(name)
	unit = strings.ToLower(unit)
	switch {
	case strings.Contains(name, "energy") && strings.Contains(unit, "kcal"):
		return "calories"
	case strings.Contains(name, "total lipid"):
		return "total_fat_g"
	case strings.Contains(name, "saturated"):
		return "saturated_fat_g"
	case strings.Contains(name, "trans"):
		return "trans_fat_g"
	case strings.Contains(name, "cholesterol"):
		return "cholesterol_mg"
	case strings.Contains(name, "sodium"):
		return "sodium_mg"
	case strings.Contains(name, "carbohydrate"):
		return "total_carbohydrate_g"
	case strings.Contains(name, "fiber"):
		return "dietary_fiber_g"
	case strings.Contains(name, "sugars"):
		return "total_sugars_g"
	case strings.Contains(name, "protein"):
		return "protein_g"
	default:
		return ""
	}
}

func setNutrient(n *enrichment.NutrientPayload, field string, value float64) {
	v := value
	switch field {
	case "calories":
		n.Calories = &v
	case "total_fat_g":
		n.TotalFatG = &v
	case "saturated_fat_g":
		n.SaturatedFatG = &v
	case "trans_fat_g":
		n.TransFatG = &v
	case "cholesterol_mg":
		n.CholesterolMG = &v
	case "sodium_mg":
		n.SodiumMG = &v
	case "total_carbohydrate_g":
		n.TotalCarbohydrateG = &v
	case "dietary_fiber_g":
		n.DietaryFiberG = &v
	case "total_sugars_g":
		n.TotalSugarsG = &v
	case "protein_g":
		n.ProteinG = &v
	}
}

func hasNutrients(n *enrichment.NutrientPayload) bool {
	return n.Calories != nil || n.TotalFatG != nil || n.SodiumMG != nil ||
		n.TotalCarbohydrateG != nil || n.DietaryFiberG != nil ||
		n.TotalSugarsG != nil || n.ProteinG != nil
}

func normalizeUSDAUPC(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func titleDescription(value string) string {
	parts := strings.Fields(strings.ToLower(value))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sourceConfidenceForSuggestions(suggestions []enrichment.Suggestion) float64 {
	if len(suggestions) == 0 {
		return 0
	}
	var total float64
	for _, s := range suggestions {
		total += s.Confidence
	}
	return total / float64(len(suggestions))
}

func stringPtr(value string) *string { return &value }

type rateLimitError string

func (e rateLimitError) Error() string { return string(e) }
