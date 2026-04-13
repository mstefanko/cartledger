package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/mealie"
	"github.com/mstefanko/cartledger/internal/units"
)

// ImportHandler holds dependencies for Mealie import endpoints.
type ImportHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// RegisterRoutes mounts import endpoints onto the protected group.
func (h *ImportHandler) RegisterRoutes(protected *echo.Group) {
	imp := protected.Group("/import/mealie")
	imp.GET("/status", h.Status)
	imp.GET("/recipes", h.ListRecipes)
	imp.POST("/recipes/:slug", h.ImportRecipe)
	imp.POST("/lists/:id", h.ImportShoppingList)
}

// mealieClient returns a Mealie client or nil if not configured.
func (h *ImportHandler) mealieClient() *mealie.Client {
	if h.Cfg.MealieURL == "" || h.Cfg.MealieToken == "" {
		return nil
	}
	return mealie.NewClient(h.Cfg.MealieURL, h.Cfg.MealieToken)
}

// notConfigured returns a standard 404 response when Mealie is not configured.
func notConfigured(c echo.Context) error {
	return c.JSON(http.StatusNotFound, map[string]string{"error": "Mealie not configured"})
}

// Status checks the Mealie connection.
// GET /api/v1/import/mealie/status
func (h *ImportHandler) Status(c echo.Context) error {
	client := h.mealieClient()
	if client == nil {
		return notConfigured(c)
	}

	if err := client.Ping(); err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status":  "connected",
		"base_url": h.Cfg.MealieURL,
	})
}

// ListRecipes returns available recipes from Mealie.
// GET /api/v1/import/mealie/recipes
func (h *ImportHandler) ListRecipes(c echo.Context) error {
	client := h.mealieClient()
	if client == nil {
		return notConfigured(c)
	}

	recipes, err := client.ListRecipes()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, recipes)
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
	RecipeName  string               `json:"recipe_name"`
	RecipeSlug  string               `json:"recipe_slug"`
	Yield       string               `json:"yield"`
	Ingredients []importedIngredient `json:"ingredients"`
	TotalCost   *string              `json:"total_cost,omitempty"`
	LinksCreated int                 `json:"links_created"`
}

// ImportRecipe fetches a recipe from Mealie, matches ingredients to the product
// catalog, creates product_links, and calculates costs.
// POST /api/v1/import/mealie/recipes/:slug
func (h *ImportHandler) ImportRecipe(c echo.Context) error {
	client := h.mealieClient()
	if client == nil {
		return notConfigured(c)
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
	client := h.mealieClient()
	if client == nil {
		return notConfigured(c)
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
