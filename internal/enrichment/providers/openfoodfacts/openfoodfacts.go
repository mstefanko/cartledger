package openfoodfacts

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mstefanko/cartledger/internal/enrichment"
	"github.com/mstefanko/cartledger/internal/enrichment/providers"
	"github.com/mstefanko/cartledger/internal/enrichment/store"
	"github.com/mstefanko/cartledger/internal/httpsafe"
)

var (
	APIBase     = "https://world.openfoodfacts.org/api/v2/product/"
	ProductBase = "https://world.openfoodfacts.org/product/"
	quantityRE  = regexp.MustCompile(`(?i)\b([0-9]+(?:[\.,][0-9]+)?)\s*(fl\s*oz|oz|lb|g|kg|ml|l|ct|count|each|ea)\b`)
)

var productFields = []string{
	"code",
	"status",
	"product_name",
	"brands",
	"quantity",
	"categories",
	"ingredients_text",
	"allergens",
	"nutriments",
	"nutrient_levels",
	"nutriscore_grade",
	"serving_size",
	"image_front_url",
	"image_nutrition_url",
	"image_ingredients_url",
}

type Provider struct {
	Client      *httpsafe.SafeHTTPClient
	APIBase     string
	ProductBase string
	Interval    time.Duration
}

func New(allowPrivate bool) *Provider {
	return &Provider{
		Client:      httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, allowPrivate),
		APIBase:     APIBase,
		ProductBase: ProductBase,
		Interval:    4 * time.Second,
	}
}

func (p *Provider) Name() string { return "openfoodfacts" }

func (p *Provider) RateLimitKey() string {
	base := p.APIBase
	if base == "" {
		base = APIBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "openfoodfacts"
	}
	return u.Host
}

func (p *Provider) MinInterval() time.Duration { return p.Interval }

func (p *Provider) Lookup(ctx context.Context, input providers.LookupInput) ([]providers.Metadata, error) {
	if input.UPC == nil || strings.TrimSpace(*input.UPC) == "" {
		return nil, nil
	}
	upc := strings.TrimSpace(*input.UPC)
	apiBase := strings.TrimRight(firstNonEmpty(p.APIBase, APIBase), "/")
	productBase := strings.TrimRight(firstNonEmpty(p.ProductBase, ProductBase), "/")

	client := p.Client
	if client == nil {
		client = httpsafe.NewSafeHTTPClient(8*time.Second, 512*1024, input.AllowPrivate)
	}
	linkURL := productBase + "/" + url.PathEscape(upc)
	var fallback *providers.Metadata
	for _, lookupCode := range openFoodFactsLookupCodes(upc) {
		sourceURL := productBase + "/" + url.PathEscape(lookupCode)
		apiURL := openFoodFactsAPIURL(apiBase, lookupCode)
		result, err := client.Fetch(ctx, apiURL)
		if err != nil {
			return nil, err
		}
		if result.StatusCode == 429 {
			return nil, providersRateLimitError("openfoodfacts rate limited")
		}

		payload, suggestions := payloadFromOpenFoodFacts(upc, sourceURL, result.Body)
		metadata := metadataFromOpenFoodFacts(upc, lookupCode, linkURL, input.LookupKey, result, payload, suggestions)
		if fallback == nil {
			item := metadata
			fallback = &item
		}
		if payloadHasProductData(payload, suggestions) {
			return []providers.Metadata{metadata}, nil
		}
	}
	if fallback != nil {
		return []providers.Metadata{*fallback}, nil
	}
	return nil, nil
}

func openFoodFactsAPIURL(apiBase, code string) string {
	endpoint := apiBase + "/" + url.PathEscape(code) + ".json"
	values := url.Values{}
	values.Set("fields", strings.Join(productFields, ","))
	return endpoint + "?" + values.Encode()
}

func metadataFromOpenFoodFacts(
	upc string,
	lookupCode string,
	sourceURL string,
	lookupKey string,
	result httpsafe.FetchResult,
	payload enrichment.MetadataPayload,
	suggestions []enrichment.Suggestion,
) providers.Metadata {
	if payload.Version == 0 {
		payload = enrichment.MetadataPayload{
			Version:        1,
			Source:         "openfoodfacts",
			SourceRecordID: stringPtr(lookupCode),
			SourceURL:      stringPtr(sourceURL),
			Identifiers: []enrichment.PayloadIdentifier{{
				Type:      "gtin",
				Authority: stringPtr("openfoodfacts"),
				Value:     upc,
			}},
		}
	}
	hash := store.HashBytes(result.Body)
	confidence := sourceConfidenceForSuggestions(suggestions)
	return providers.Metadata{
		Source:         "openfoodfacts",
		SourceRecordID: sourceRecordIDForPayload(payload, lookupCode),
		SourceURL:      sourceURL,
		LookupKey:      lookupKey,
		Confidence:     confidence,
		Payload:        payload,
		Suggestions:    suggestions,
		FetchedAt:      result.FetchedAt,
		HTTPStatus:     result.StatusCode,
		ContentHash:    &hash,
	}
}

func payloadFromOpenFoodFacts(upc, sourceURL string, body []byte) (enrichment.MetadataPayload, []enrichment.Suggestion) {
	var raw struct {
		Status  int `json:"status"`
		Product struct {
			ProductName      string            `json:"product_name"`
			Brands           string            `json:"brands"`
			Code             string            `json:"code"`
			Quantity         string            `json:"quantity"`
			Categories       string            `json:"categories"`
			IngredientsText  string            `json:"ingredients_text"`
			Allergens        string            `json:"allergens"`
			Nutriments       map[string]any    `json:"nutriments"`
			NutrientLevels   map[string]string `json:"nutrient_levels"`
			NutriScoreGrade  string            `json:"nutriscore_grade"`
			ServingSize      string            `json:"serving_size"`
			ImageFrontURL    string            `json:"image_front_url"`
			ImageNutrition   string            `json:"image_nutrition_url"`
			ImageIngredients string            `json:"image_ingredients_url"`
		} `json:"product"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Status != 1 {
		return enrichment.MetadataPayload{}, nil
	}

	sourceRecordID := strings.TrimSpace(raw.Product.Code)
	if sourceRecordID == "" {
		sourceRecordID = upc
	}
	payload := enrichment.MetadataPayload{
		Version:        1,
		Source:         "openfoodfacts",
		SourceRecordID: &sourceRecordID,
		SourceURL:      &sourceURL,
		Identifiers: []enrichment.PayloadIdentifier{{
			Type:      "gtin",
			Authority: stringPtr("openfoodfacts"),
			Value:     upc,
		}},
		Evidence: []enrichment.EvidencePayload{},
	}

	var suggestions []enrichment.Suggestion
	add := func(field, value, evidence string, confidence float64) {
		value = strings.TrimSpace(value)
		if value != "" {
			suggestions = append(suggestions, enrichment.NewSuggestion("openfoodfacts", sourceURL, field, value, evidence, confidence))
		}
	}
	add("upc", upc, "Open Food Facts barcode match", 0.86)
	if name := strings.TrimSpace(raw.Product.ProductName); name != "" {
		payload.Name = &name
		add("name", name, name, 0.72)
	}
	if brand := strings.TrimSpace(strings.Split(raw.Product.Brands, ",")[0]); brand != "" {
		payload.Brand = &brand
		add("brand", brand, raw.Product.Brands, 0.68)
	}
	if category := strings.TrimSpace(strings.Split(raw.Product.Categories, ",")[0]); category != "" {
		payload.Category = &category
	}
	if qty, unit := parseQuantity(raw.Product.Quantity); qty != "" && unit != "" {
		quantity, _ := strconv.ParseFloat(qty, 64)
		payload.Package = &enrichment.PackagePayload{
			Label:    stringPtr(raw.Product.Quantity),
			Quantity: &quantity,
			Unit:     &unit,
		}
		payload.Evidence = append(payload.Evidence, enrichment.EvidencePayload{Field: "package", Text: raw.Product.Quantity, URL: &sourceURL})
		add("pack_quantity", qty, raw.Product.Quantity, 0.68)
		add("pack_unit", unit, raw.Product.Quantity, 0.68)
	}
	if serving := strings.TrimSpace(raw.Product.ServingSize); serving != "" {
		payload.Serving = &enrichment.ServingPayload{Label: &serving}
		add("serving_label", serving, serving, 0.66)
	}
	if ingredients := strings.TrimSpace(raw.Product.IngredientsText); ingredients != "" {
		payload.Ingredients = &ingredients
		add("ingredients", ingredients, "Ingredients: "+ingredients, 0.68)
	}
	if allergens := splitAllergens(raw.Product.Allergens); len(allergens) > 0 {
		payload.Allergens = allergens
		add("allergens", strings.Join(allergens, ", "), raw.Product.Allergens, 0.62)
	}
	nutrients := nutrientPayloadFromOFF(numericNutriments(raw.Product.Nutriments), add)
	if nutrients != nil {
		payload.Nutrients = nutrients
	}
	imageURLs := map[string]string{}
	if raw.Product.ImageFrontURL != "" {
		imageURLs["front"] = raw.Product.ImageFrontURL
		add("image_front_url", raw.Product.ImageFrontURL, "Open Food Facts front photo", 0.68)
	}
	if raw.Product.ImageNutrition != "" {
		imageURLs["nutrition"] = raw.Product.ImageNutrition
		add("image_nutrition_url", raw.Product.ImageNutrition, "Open Food Facts nutrition photo", 0.68)
	}
	if raw.Product.ImageIngredients != "" {
		imageURLs["ingredients"] = raw.Product.ImageIngredients
		add("image_ingredients_url", raw.Product.ImageIngredients, "Open Food Facts ingredients photo", 0.68)
	}
	if len(imageURLs) > 0 {
		payload.ImageURLs = imageURLs
	}
	providerMeta := map[string]string{}
	if grade := strings.TrimSpace(raw.Product.NutriScoreGrade); grade != "" {
		providerMeta["nutriscore_grade"] = strings.ToUpper(grade)
	}
	for name, level := range raw.Product.NutrientLevels {
		name = strings.TrimSpace(name)
		level = strings.TrimSpace(level)
		if name != "" && level != "" {
			providerMeta["nutrient_level_"+name] = level
		}
	}
	if len(providerMeta) > 0 {
		payload.ProviderMeta = providerMeta
	}
	return payload, suggestions
}

func openFoodFactsLookupCodes(upc string) []string {
	added := map[string]struct{}{}
	out := make([]string, 0, 2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := added[value]; ok {
			return
		}
		added[value] = struct{}{}
		out = append(out, value)
	}
	add(upc)
	if len(upc) == 12 {
		add("0" + upc)
	}
	if len(upc) == 13 && strings.HasPrefix(upc, "0") {
		add(strings.TrimPrefix(upc, "0"))
	}
	return out
}

func payloadHasProductData(payload enrichment.MetadataPayload, suggestions []enrichment.Suggestion) bool {
	if payload.Version == 0 {
		return false
	}
	if payload.Name != nil || payload.Brand != nil || payload.Category != nil ||
		payload.Package != nil || payload.Serving != nil || payload.Nutrients != nil ||
		payload.Ingredients != nil || len(payload.Allergens) > 0 ||
		len(payload.ImageURLs) > 0 || len(payload.ProviderMeta) > 0 {
		return true
	}
	for _, suggestion := range suggestions {
		if strings.TrimSpace(suggestion.Field) != "" && suggestion.Field != "upc" {
			return true
		}
	}
	return false
}

func sourceRecordIDForPayload(payload enrichment.MetadataPayload, fallback string) *string {
	if payload.SourceRecordID != nil {
		if value := strings.TrimSpace(*payload.SourceRecordID); value != "" {
			return &value
		}
	}
	return stringPtr(fallback)
}

func nutrientPayloadFromOFF(nutriments map[string]float64, add func(string, string, string, float64)) *enrichment.NutrientPayload {
	if len(nutriments) == 0 {
		return nil
	}
	out := &enrichment.NutrientPayload{}
	set := func(key, field string, target **float64) {
		if value, ok := nutriments[key]; ok && value > 0 {
			v := value
			*target = &v
			add(field, strconv.FormatFloat(value, 'f', -1, 64), key, 0.65)
		}
	}
	set("energy-kcal_serving", "calories", &out.Calories)
	set("fat_serving", "total_fat_g", &out.TotalFatG)
	set("saturated-fat_serving", "saturated_fat_g", &out.SaturatedFatG)
	set("trans-fat_serving", "trans_fat_g", &out.TransFatG)
	set("cholesterol_serving", "cholesterol_mg", &out.CholesterolMG)
	set("sodium_serving", "sodium_mg", &out.SodiumMG)
	set("carbohydrates_serving", "total_carbohydrate_g", &out.TotalCarbohydrateG)
	set("fiber_serving", "dietary_fiber_g", &out.DietaryFiberG)
	set("sugars_serving", "total_sugars_g", &out.TotalSugarsG)
	set("proteins_serving", "protein_g", &out.ProteinG)
	if out.Calories == nil && out.TotalFatG == nil && out.SodiumMG == nil && out.ProteinG == nil &&
		out.TotalCarbohydrateG == nil && out.DietaryFiberG == nil && out.TotalSugarsG == nil {
		return nil
	}
	return out
}

func numericNutriments(values map[string]any) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, len(values))
	for key, value := range values {
		switch v := value.(type) {
		case float64:
			out[key] = v
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil {
				out[key] = parsed
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func splitAllergens(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(part, "en:"))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseQuantity(value string) (string, string) {
	m := quantityRE.FindStringSubmatch(value)
	if len(m) != 3 {
		return "", ""
	}
	return strings.ReplaceAll(m[1], ",", "."), canonicalProductUnit(m[2])
}

func canonicalProductUnit(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	switch normalized {
	case "fl oz":
		return "fl_oz"
	case "ct", "count", "ea":
		return "each"
	}
	return normalized
}

type rateLimitError string

func (e rateLimitError) Error() string { return string(e) }

func providersRateLimitError(msg string) error { return rateLimitError(msg) }
