package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
	"github.com/mstefanko/cartledger/internal/mealie"
	"github.com/mstefanko/cartledger/internal/models"
	"github.com/mstefanko/cartledger/internal/units"
)

// errMealieNotConfigured is returned by loadMealieClient when the household
// has no Mealie integration row, or the row exists but is disabled. Handlers
// branch on errors.Is to translate into the frontend-visible status shape
// without leaking DB-layer errors.
var errMealieNotConfigured = errors.New("mealie integration not configured")

// ImportHandler holds dependencies for Mealie import endpoints.
type ImportHandler struct {
	DB           *sql.DB
	Cfg          *config.Config
	Integrations *db.IntegrationStore
}

// RegisterRoutes mounts import endpoints onto the protected group.
func (h *ImportHandler) RegisterRoutes(protected *echo.Group) {
	imp := protected.Group("/import/mealie")
	imp.GET("/status", h.Status)
	imp.GET("/recipes", h.ListRecipes)
	imp.POST("/recipes/:slug", h.ImportRecipe)
	imp.GET("/lists", h.ListShoppingLists)
	imp.POST("/lists/:id", h.ImportShoppingList)
}

// integrationStore returns the backing store, lazily constructing one from
// h.DB if the handler was built without wiring the store explicitly. This
// keeps existing callers that zero-init the handler (e.g. older tests) working.
func (h *ImportHandler) integrationStore() *db.IntegrationStore {
	if h.Integrations != nil {
		return h.Integrations
	}
	return db.NewIntegrationStore(h.DB)
}

// loadMealieClient reads the household's Mealie integration row and returns
// a configured *mealie.Client along with the base URL (for status responses).
// Returns errMealieNotConfigured if no row exists, the row is disabled, or
// the stored credentials are empty/unparseable.
func (h *ImportHandler) loadMealieClient(c echo.Context) (*mealie.Client, string, error) {
	householdID := auth.HouseholdIDFrom(c)
	if householdID == "" {
		return nil, "", errMealieNotConfigured
	}

	row, err := h.integrationStore().GetByType(c.Request().Context(), householdID, models.IntegrationTypeMealie)
	if err != nil {
		return nil, "", err
	}
	if row == nil || !row.Enabled {
		return nil, "", errMealieNotConfigured
	}

	var cfg models.MealieConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, "", errMealieNotConfigured
	}
	if cfg.BaseURL == "" || cfg.Token == "" {
		return nil, "", errMealieNotConfigured
	}

	return mealie.NewClient(cfg.BaseURL, cfg.Token), cfg.BaseURL, nil
}

// Status checks the Mealie connection.
// GET /api/v1/import/mealie/status
//
// Always returns 200 with the shape the frontend consumes:
//
//	{configured: bool, connected: bool, mealie_url: string|null, error: string|null}
//
// Non-configured and connection-error cases are distinguished by `configured`
// and `error` fields rather than by HTTP status, matching web/src/pages/ImportPage.tsx:138.
func (h *ImportHandler) Status(c echo.Context) error {
	client, baseURL, err := h.loadMealieClient(c)
	if errors.Is(err, errMealieNotConfigured) {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"configured": false,
			"connected":  false,
			"mealie_url": nil,
			"error":      nil,
		})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"configured": false,
			"connected":  false,
			"mealie_url": nil,
			"error":      "internal error",
		})
	}

	if err := client.Ping(); err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"configured": true,
			"connected":  false,
			"mealie_url": baseURL,
			"error":      err.Error(),
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"configured": true,
		"connected":  true,
		"mealie_url": baseURL,
		"error":      nil,
	})
}

// notConfiguredResponse returns a 400 in the frontend-compatible shape for
// non-status endpoints. Callers that only need connectivity use this to keep
// copy consistent with the ConnectionStatus banner on ImportPage.
func notConfiguredResponse(c echo.Context) error {
	return c.JSON(http.StatusBadRequest, map[string]interface{}{
		"error":      "Mealie not configured",
		"configured": false,
	})
}

// ListRecipes returns available recipes from Mealie.
// GET /api/v1/import/mealie/recipes
func (h *ImportHandler) ListRecipes(c echo.Context) error {
	client, _, err := h.loadMealieClient(c)
	if errors.Is(err, errMealieNotConfigured) {
		return notConfiguredResponse(c)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	recipes, err := client.ListRecipes()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, recipes)
}

// ListShoppingLists returns shopping lists from Mealie. The frontend calls this
// via web/src/api/import.ts:22; the handler was previously unregistered.
// GET /api/v1/import/mealie/lists
func (h *ImportHandler) ListShoppingLists(c echo.Context) error {
	client, _, err := h.loadMealieClient(c)
	if errors.Is(err, errMealieNotConfigured) {
		return notConfiguredResponse(c)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	lists, err := client.ListShoppingLists()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, lists)
}

// --- Import recipe types ---

type importedIngredient struct {
	Display   string  `json:"display"`
	FoodName  string  `json:"food_name"`
	Quantity  string  `json:"quantity"`
	Unit      string  `json:"unit"`
	ProductID *string `json:"product_id,omitempty"`
	Matched   bool    `json:"matched"`
	UnitCost  *string `json:"unit_cost,omitempty"`
	TotalCost *string `json:"total_cost,omitempty"`
}

type importedRecipeResponse struct {
	RecipeName   string               `json:"recipe_name"`
	RecipeSlug   string               `json:"recipe_slug"`
	Yield        string               `json:"yield"`
	Ingredients  []importedIngredient `json:"ingredients"`
	TotalCost    *string              `json:"total_cost,omitempty"`
	LinksCreated int                  `json:"links_created"`
}

// ImportRecipe fetches a recipe from Mealie, matches ingredients to the product
// catalog, creates product_links, and calculates costs.
// POST /api/v1/import/mealie/recipes/:slug
func (h *ImportHandler) ImportRecipe(c echo.Context) error {
	client, _, err := h.loadMealieClient(c)
	if errors.Is(err, errMealieNotConfigured) {
		return notConfiguredResponse(c)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	householdID := auth.HouseholdIDFrom(c)
	slug := c.Param("slug")

	recipe, err := client.GetRecipe(slug)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	now := time.Now().UTC()
	totalCost := decimal.Zero
	hasCost := false
	linksCreated := 0
	ingredients := make([]importedIngredient, 0, len(recipe.Ingredients))

	for _, ing := range recipe.Ingredients {
		item := importedIngredient{
			Display:  ing.Display,
			Quantity: decimal.NewFromFloat(ing.Quantity).String(),
		}

		if ing.Food != nil {
			item.FoodName = ing.Food.Name
		}
		if ing.Unit != nil {
			item.Unit = units.NormalizeUnit(ing.Unit.Name)
		}

		// Try to match ingredient to product catalog.
		if ing.Food != nil && ing.Food.Name != "" {
			productID, err := h.matchOrCreateProduct(householdID, ing.Food.Name, now)
			if err == nil && productID != "" {
				item.ProductID = &productID
				item.Matched = true

				// Create product_link for mealie_food + mealie_recipe.
				if ing.Food != nil {
					h.ensureProductLink(productID, "mealie_food", ing.Food.ID, ing.Food.Name, now)
					linksCreated++
				}
				h.ensureProductLink(productID, "mealie_recipe", recipe.ID, recipe.Name+" - "+ing.Food.Name, now)

				// Calculate cost from latest price.
				cost := h.calculateIngredientCost(productID, ing.Quantity, item.Unit)
				if cost != nil {
					uc := cost.unitCost.StringFixed(2)
					tc := cost.totalCost.StringFixed(2)
					item.UnitCost = &uc
					item.TotalCost = &tc
					totalCost = totalCost.Add(cost.totalCost)
					hasCost = true
				}
			}
		}

		ingredients = append(ingredients, item)
	}

	resp := importedRecipeResponse{
		RecipeName:   recipe.Name,
		RecipeSlug:   recipe.Slug,
		Yield:        recipe.RecipeYield,
		Ingredients:  ingredients,
		LinksCreated: linksCreated,
	}
	if hasCost {
		tc := totalCost.StringFixed(2)
		resp.TotalCost = &tc
	}

	return c.JSON(http.StatusOK, resp)
}

// matchOrCreateProduct finds a product by name or creates a new one.
func (h *ImportHandler) matchOrCreateProduct(householdID, foodName string, now time.Time) (string, error) {
	foodName = strings.TrimSpace(foodName)
	if foodName == "" {
		return "", nil
	}

	// Try exact name match first.
	var productID string
	err := h.DB.QueryRow(
		"SELECT id FROM products WHERE household_id = ? AND LOWER(name) = LOWER(?)",
		householdID, foodName,
	).Scan(&productID)
	if err == nil {
		return productID, nil
	}

	// Try alias match.
	err = h.DB.QueryRow(
		`SELECT p.id FROM products p
		 JOIN product_aliases pa ON p.id = pa.product_id
		 WHERE p.household_id = ? AND LOWER(pa.alias) = LOWER(?)`,
		householdID, foodName,
	).Scan(&productID)
	if err == nil {
		return productID, nil
	}

	// Create new product from food name.
	productID = uuid.New().String()
	_, err = h.DB.Exec(
		`INSERT INTO products (id, household_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		productID, householdID, foodName, now, now,
	)
	if err != nil {
		return "", err
	}

	return productID, nil
}

// ensureProductLink creates a product_link if one doesn't already exist.
func (h *ImportHandler) ensureProductLink(productID, source, externalID, label string, now time.Time) {
	var exists int
	err := h.DB.QueryRow(
		"SELECT COUNT(*) FROM product_links WHERE product_id = ? AND source = ? AND external_id = ?",
		productID, source, externalID,
	).Scan(&exists)
	if err != nil || exists > 0 {
		return
	}

	_, _ = h.DB.Exec(
		`INSERT INTO product_links (id, product_id, source, external_id, url, label, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), productID, source, externalID, "", label, now,
	)
}

type ingredientCost struct {
	unitCost  decimal.Decimal
	totalCost decimal.Decimal
}

// calculateIngredientCost looks up the latest price for a product and converts
// to the ingredient's unit to estimate cost.
func (h *ImportHandler) calculateIngredientCost(productID string, quantity float64, unit string) *ingredientCost {
	if quantity <= 0 {
		return nil
	}

	// Get the latest product price.
	var unitPriceStr, priceUnit string
	var priceQtyStr string
	err := h.DB.QueryRow(
		`SELECT unit_price, unit, quantity FROM product_prices
		 WHERE product_id = ? ORDER BY receipt_date DESC LIMIT 1`,
		productID,
	).Scan(&unitPriceStr, &priceUnit, &priceQtyStr)
	if err != nil {
		return nil
	}

	unitPrice, _ := decimal.NewFromString(unitPriceStr)
	priceQty, _ := decimal.NewFromString(priceQtyStr)
	if unitPrice.IsZero() || priceQty.IsZero() {
		return nil
	}

	// Price is per priceQty of priceUnit. We need cost for quantity of unit.
	ingredientQty := decimal.NewFromFloat(quantity)
	unit = units.NormalizeUnit(unit)
	priceUnit = units.NormalizeUnit(priceUnit)

	// Convert ingredient quantity to the price's unit.
	convertedQty, err := units.Convert(ingredientQty, unit, priceUnit, productID, h.DB)
	if err != nil {
		// If we can't convert, try using normalized prices instead.
		return nil
	}

	// Cost = (convertedQty / priceQty) * unitPrice * priceQty = convertedQty * unitPrice
	cost := convertedQty.Mul(unitPrice)

	return &ingredientCost{
		unitCost:  unitPrice,
		totalCost: cost,
	}
}

// ImportShoppingList imports a Mealie shopping list as a CartLedger shopping list.
// POST /api/v1/import/mealie/lists/:id
func (h *ImportHandler) ImportShoppingList(c echo.Context) error {
	client, _, err := h.loadMealieClient(c)
	if errors.Is(err, errMealieNotConfigured) {
		return notConfiguredResponse(c)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	householdID := auth.HouseholdIDFrom(c)
	userID := auth.UserIDFrom(c)
	listID := c.Param("id")

	mealieList, err := client.GetShoppingList(listID)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	now := time.Now().UTC()

	// Create CartLedger shopping list.
	newListID := uuid.New().String()
	_, err = h.DB.Exec(
		`INSERT INTO shopping_lists (id, household_id, name, created_by, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		newListID, householdID, mealieList.Name, userID, now, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create shopping list"})
	}

	// Import items.
	itemsCreated := 0
	for i, item := range mealieList.Items {
		if item.Checked {
			continue
		}

		itemName := item.Display
		if item.Food != nil {
			itemName = item.Food.Name
		}
		if itemName == "" {
			itemName = item.Note
		}
		if itemName == "" {
			continue
		}

		var productID *string
		if item.Food != nil && item.Food.Name != "" {
			pid, err := h.matchOrCreateProduct(householdID, item.Food.Name, now)
			if err == nil && pid != "" {
				productID = &pid
			}
		}

		qty := decimal.NewFromFloat(item.Quantity)
		if qty.IsZero() {
			qty = decimal.NewFromInt(1)
		}

		var unitStr *string
		if item.Unit != nil {
			normalized := units.NormalizeUnit(item.Unit.Name)
			unitStr = &normalized
		}

		itemID := uuid.New().String()
		_, err = h.DB.Exec(
			`INSERT INTO shopping_list_items (id, list_id, product_id, name, quantity, unit, checked, sort_order, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			itemID, newListID, productID, itemName, qty.String(), unitStr, i, now,
		)
		if err != nil {
			continue
		}
		itemsCreated++
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"list_id":       newListID,
		"name":          mealieList.Name,
		"items_created": itemsCreated,
	})
}
