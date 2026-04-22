package api

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// AnalyticsHandler holds dependencies for analytics endpoints.
type AnalyticsHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// RegisterRoutes mounts analytics endpoints onto the protected group.
func (h *AnalyticsHandler) RegisterRoutes(protected *echo.Group) {
	analytics := protected.Group("/analytics")
	analytics.GET("/overview", h.Overview)
	analytics.GET("/products/:id/trend", h.ProductTrend)
	analytics.GET("/products", h.ProductsWithTrends)
	analytics.GET("/stores/:id/summary", h.StoreSummary)
	analytics.GET("/trips", h.Trips)
	analytics.GET("/deals", h.Deals)
	analytics.GET("/buy-again", h.BuyAgain)
	analytics.GET("/rhythm", h.Rhythm)
	analytics.GET("/product-groups/:id/trend", h.GroupTrend)
	analytics.GET("/category-breakdown", h.CategoryBreakdown)
	analytics.GET("/savings", h.Savings)
	analytics.GET("/staples", h.Staples)
	analytics.GET("/price-moves", h.PriceMoves)
	analytics.GET("/inflation", h.Inflation)
}

// --- Response types ---

type overviewResponse struct {
	SpentThisMonth float64 `json:"spent_this_month"`
	SpentLastMonth float64 `json:"spent_last_month"`
	PercentChange  float64 `json:"percent_change"`
	TripCount      int     `json:"trip_count"`
	AvgTripCost    float64 `json:"avg_trip_cost"`
	UniqueProducts int     `json:"unique_products_purchased"`
}

type savingsResponse struct {
	MonthToDate float64 `json:"month_to_date"`
	Last30D     float64 `json:"last_30d"`
	YearToDate  float64 `json:"year_to_date"`
}

type rhythmTrips struct {
	Current  int      `json:"current"`
	Prior    int      `json:"prior"`
	DeltaPct *float64 `json:"delta_pct"`
}

type rhythmBasket struct {
	Current float64 `json:"current"`
	Prior   float64 `json:"prior"`
}

type rhythmResponse struct {
	Trips           rhythmTrips  `json:"trips"`
	AvgBasket       rhythmBasket `json:"avg_basket"`
	AvgItemsPerTrip float64      `json:"avg_items_per_trip"`
	MostShoppedDOW  *string      `json:"most_shopped_dow"`
	HistoryDays     int          `json:"history_days"`
}

type pricePoint struct {
	Date            string  `json:"date"`
	NormalizedPrice float64 `json:"normalized_price"`
	Store           string  `json:"store"`
	IsSale          bool    `json:"is_sale"`
}

type productTrendResponse struct {
	PriceHistory  []pricePoint `json:"price_history"`
	PercentChange float64      `json:"percent_change"`
	MinPrice      float64      `json:"min_price"`
	MinStore      string       `json:"min_store"`
	MaxPrice      float64      `json:"max_price"`
	MaxStore      string       `json:"max_store"`
	AvgPrice      float64      `json:"avg_price"`
}

type productTrendItem struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Category      *string  `json:"category,omitempty"`
	LatestPrice   float64  `json:"latest_price"`
	AvgPrice      float64  `json:"avg_price"`
	PercentChange float64  `json:"percent_change"`
	LastPurchased *string  `json:"last_purchased,omitempty"`
}

type productTrendsResponse struct {
	Products []productTrendItem `json:"products"`
	Total    int                `json:"total"`
}

type storePriceLeader struct {
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name"`
	AvgPrice    float64 `json:"avg_price"`
}

type storeRecentTrip struct {
	ReceiptID   string  `json:"receipt_id"`
	Date        string  `json:"date"`
	Total       float64 `json:"total"`
	ItemCount   int     `json:"item_count"`
}

type storeSummaryStore struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Icon        *string `json:"icon"`
	Nickname    *string `json:"nickname"`
	StoreNumber *string `json:"store_number"`
	Address     *string `json:"address"`
	City        *string `json:"city"`
	State       *string `json:"state"`
	Zip         *string `json:"zip"`
}

type storeSummaryResponse struct {
	Store        storeSummaryStore  `json:"store"`
	TotalSpent   float64            `json:"total_spent"`
	TripCount    int                `json:"trip_count"`
	AvgTripCost  float64            `json:"avg_trip_cost"`
	PriceLeaders []storePriceLeader `json:"price_leaders"`
	RecentTrips  []storeRecentTrip  `json:"recent_trips"`
}

type tripItem struct {
	ReceiptID string  `json:"receipt_id"`
	Date      string  `json:"date"`
	StoreID   *string `json:"store_id"`
	StoreName string  `json:"store_name"`
	Total     float64 `json:"total"`
	ItemCount int     `json:"item_count"`
}

type dealItem struct {
	ProductID      string  `json:"product_id"`
	ProductName    string  `json:"product_name"`
	Store          string  `json:"store"`
	LatestPrice    float64 `json:"latest_price"`
	AvgPrice       float64 `json:"avg_price"`
	SavingsPercent float64 `json:"savings_percent"`
	IsSale         bool    `json:"is_sale"`
}

type buyAgainItem struct {
	ProductID      string  `json:"product_id"`
	ProductName    string  `json:"product_name"`
	AvgDaysPerUnit float64 `json:"avg_days_per_unit"`
	EstSupplyDays  float64 `json:"est_supply_days"`
	DaysSinceLast  float64 `json:"days_since_last"`
	UrgencyRatio   float64 `json:"urgency_ratio"`
	Urgency        string  `json:"urgency"`
	LastPrice      *string `json:"last_price,omitempty"`
	LastStoreName  *string `json:"last_store_name,omitempty"`
}

// Overview returns spending summary for the household.
// GET /api/v1/analytics/overview
func (h *AnalyticsHandler) Overview(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	now := time.Now().UTC()

	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	lastMonthStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	resp := overviewResponse{}

	// Total spent this month.
	h.DB.QueryRow(
		`SELECT COALESCE(SUM(CAST(total AS REAL)), 0), COUNT(*)
		 FROM receipts
		 WHERE household_id = ? AND receipt_date >= ?`,
		householdID, thisMonthStart,
	).Scan(&resp.SpentThisMonth, &resp.TripCount)
	resp.SpentThisMonth = math.Round(resp.SpentThisMonth*100) / 100

	// Total spent last month.
	h.DB.QueryRow(
		`SELECT COALESCE(SUM(CAST(total AS REAL)), 0)
		 FROM receipts
		 WHERE household_id = ? AND receipt_date >= ? AND receipt_date < ?`,
		householdID, lastMonthStart, thisMonthStart,
	).Scan(&resp.SpentLastMonth)
	resp.SpentLastMonth = math.Round(resp.SpentLastMonth*100) / 100

	// Month-over-month change %.
	if resp.SpentLastMonth > 0 {
		resp.PercentChange = math.Round(((resp.SpentThisMonth-resp.SpentLastMonth)/resp.SpentLastMonth)*10000) / 100
	}

	// Avg trip cost this month.
	if resp.TripCount > 0 {
		resp.AvgTripCost = math.Round(resp.SpentThisMonth/float64(resp.TripCount)*100) / 100
	}

	// Unique products purchased this month.
	h.DB.QueryRow(
		`SELECT COUNT(DISTINCT pp.product_id)
		 FROM product_prices pp
		 JOIN products p ON p.id = pp.product_id
		 WHERE p.household_id = ? AND pp.receipt_date >= ?`,
		householdID, thisMonthStart,
	).Scan(&resp.UniqueProducts)

	return c.JSON(http.StatusOK, resp)
}

// ProductTrend returns price history and stats for a single product.
// GET /api/v1/analytics/products/:id/trend
func (h *AnalyticsHandler) ProductTrend(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	productID := c.Param("id")

	// Verify product belongs to household.
	var exists int
	err := h.DB.QueryRow(
		"SELECT 1 FROM products WHERE id = ? AND household_id = ?",
		productID, householdID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	sixMonthsAgo := time.Now().UTC().AddDate(0, -6, 0).Format("2006-01-02")

	// Price history (last 6 months).
	rows, err := h.DB.Query(
		`SELECT pp.receipt_date,
		        COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price,
		        s.name, pp.is_sale
		 FROM product_prices pp
		 JOIN stores s ON pp.store_id = s.id
		 WHERE pp.product_id = ? AND pp.receipt_date >= ?
		 ORDER BY pp.receipt_date ASC`,
		productID, sixMonthsAgo,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	history := make([]pricePoint, 0)
	for rows.Next() {
		var pp pricePoint
		var receiptDate time.Time
		if err := rows.Scan(&receiptDate, &pp.NormalizedPrice, &pp.Store, &pp.IsSale); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		pp.Date = receiptDate.Format("2006-01-02")
		history = append(history, pp)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp := productTrendResponse{
		PriceHistory: history,
	}

	// Percent change: first vs last in the window.
	if len(history) >= 2 {
		first := history[0].NormalizedPrice
		last := history[len(history)-1].NormalizedPrice
		if first > 0 {
			resp.PercentChange = math.Round(((last-first)/first)*10000) / 100
		}
	}

	// Min/max/avg with store names.
	h.DB.QueryRow(
		`SELECT COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price, s.name
		 FROM product_prices pp
		 JOIN stores s ON pp.store_id = s.id
		 WHERE pp.product_id = ? AND pp.receipt_date >= ?
		 ORDER BY price ASC LIMIT 1`,
		productID, sixMonthsAgo,
	).Scan(&resp.MinPrice, &resp.MinStore)

	h.DB.QueryRow(
		`SELECT COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price, s.name
		 FROM product_prices pp
		 JOIN stores s ON pp.store_id = s.id
		 WHERE pp.product_id = ? AND pp.receipt_date >= ?
		 ORDER BY price DESC LIMIT 1`,
		productID, sixMonthsAgo,
	).Scan(&resp.MaxPrice, &resp.MaxStore)

	h.DB.QueryRow(
		`SELECT AVG(COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)))
		 FROM product_prices pp
		 WHERE pp.product_id = ? AND pp.receipt_date >= ?`,
		productID, sixMonthsAgo,
	).Scan(&resp.AvgPrice)
	resp.AvgPrice = math.Round(resp.AvgPrice*100) / 100

	return c.JSON(http.StatusOK, resp)
}

// ProductsWithTrends returns all products with trend data, paginated and sortable.
// GET /api/v1/analytics/products?sort=price_change&order=desc&limit=50&offset=0
func (h *AnalyticsHandler) ProductsWithTrends(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	if offset < 0 {
		offset = 0
	}

	sortField := c.QueryParam("sort")
	order := c.QueryParam("order")
	if order != "asc" {
		order = "desc"
	}

	// Map sort fields to SQL.
	orderClause := "p.name ASC"
	switch sortField {
	case "price_change":
		orderClause = fmt.Sprintf("percent_change %s", order)
	case "latest_price":
		orderClause = fmt.Sprintf("latest_price %s", order)
	case "avg_price":
		orderClause = fmt.Sprintf("avg_price %s", order)
	case "name":
		orderClause = fmt.Sprintf("p.name %s", order)
	case "last_purchased":
		orderClause = fmt.Sprintf("p.last_purchased_at %s NULLS LAST", order)
	}

	// Count total.
	var total int
	h.DB.QueryRow(
		"SELECT COUNT(*) FROM products WHERE household_id = ?",
		householdID,
	).Scan(&total)

	query := fmt.Sprintf(`
		SELECT p.id, p.name, p.category, p.last_purchased_at,
		       COALESCE(latest.price, 0) as latest_price,
		       COALESCE(stats.avg_price, 0) as avg_price,
		       CASE WHEN COALESCE(stats.first_price, 0) > 0
		            THEN ROUND(((COALESCE(latest.price, 0) - stats.first_price) / stats.first_price) * 100, 2)
		            ELSE 0 END as percent_change
		FROM products p
		LEFT JOIN (
		    SELECT pp.product_id,
		           MAX(COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL))) as price
		    FROM product_prices pp
		    WHERE pp.receipt_date = (
		        SELECT MAX(pp2.receipt_date)
		        FROM product_prices pp2
		        WHERE pp2.product_id = pp.product_id
		    )
		    GROUP BY pp.product_id
		) latest ON latest.product_id = p.id
		LEFT JOIN (
		    SELECT pp.product_id,
		           AVG(COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL))) as avg_price,
		           (SELECT COALESCE(CAST(pp3.normalized_price AS REAL), CAST(pp3.unit_price AS REAL))
		            FROM product_prices pp3
		            WHERE pp3.product_id = pp.product_id
		            ORDER BY pp3.receipt_date ASC LIMIT 1) as first_price
		    FROM product_prices pp
		    GROUP BY pp.product_id
		) stats ON stats.product_id = p.id
		WHERE p.household_id = ?
		ORDER BY %s
		LIMIT ? OFFSET ?`, orderClause)

	rows, err := h.DB.Query(query, householdID, limit, offset)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	products := make([]productTrendItem, 0)
	for rows.Next() {
		var item productTrendItem
		var lastPurchased *time.Time
		if err := rows.Scan(&item.ID, &item.Name, &item.Category, &lastPurchased,
			&item.LatestPrice, &item.AvgPrice, &item.PercentChange); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if lastPurchased != nil {
			s := lastPurchased.Format("2006-01-02")
			item.LastPurchased = &s
		}
		item.AvgPrice = math.Round(item.AvgPrice*100) / 100
		item.LatestPrice = math.Round(item.LatestPrice*100) / 100
		products = append(products, item)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, productTrendsResponse{
		Products: products,
		Total:    total,
	})
}

// StoreSummary returns spending and trip data for a specific store.
// GET /api/v1/analytics/stores/:id/summary
func (h *AnalyticsHandler) StoreSummary(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	storeID := c.Param("id")

	// Fetch store details and verify it belongs to household.
	var store storeSummaryStore
	err := h.DB.QueryRow(
		`SELECT id, name, icon, nickname, store_number, address, city, state, zip
		 FROM stores WHERE id = ? AND household_id = ?`,
		storeID, householdID,
	).Scan(&store.ID, &store.Name, &store.Icon, &store.Nickname,
		&store.StoreNumber, &store.Address, &store.City, &store.State, &store.Zip)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "store not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp := storeSummaryResponse{
		Store:        store,
		PriceLeaders: make([]storePriceLeader, 0),
		RecentTrips:  make([]storeRecentTrip, 0),
	}

	// Total spent and trip count.
	h.DB.QueryRow(
		`SELECT COALESCE(SUM(CAST(total AS REAL)), 0), COUNT(*)
		 FROM receipts
		 WHERE store_id = ? AND household_id = ?`,
		storeID, householdID,
	).Scan(&resp.TotalSpent, &resp.TripCount)

	if resp.TripCount > 0 {
		resp.AvgTripCost = math.Round(resp.TotalSpent/float64(resp.TripCount)*100) / 100
	}
	resp.TotalSpent = math.Round(resp.TotalSpent*100) / 100

	// Price leaders: top 5 cheapest products at this store (by avg normalized price).
	leaderRows, err := h.DB.Query(
		`SELECT pp.product_id, p.name,
		        AVG(COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL))) as avg_price
		 FROM product_prices pp
		 JOIN products p ON p.id = pp.product_id
		 WHERE pp.store_id = ? AND p.household_id = ? AND p.is_non_product = 0
		 GROUP BY pp.product_id
		 ORDER BY avg_price ASC
		 LIMIT 5`,
		storeID, householdID,
	)
	if err == nil {
		defer leaderRows.Close()
		for leaderRows.Next() {
			var leader storePriceLeader
			if leaderRows.Scan(&leader.ProductID, &leader.ProductName, &leader.AvgPrice) == nil {
				leader.AvgPrice = math.Round(leader.AvgPrice*100) / 100
				resp.PriceLeaders = append(resp.PriceLeaders, leader)
			}
		}
	}

	// Recent trips (last 10).
	tripRows, err := h.DB.Query(
		`SELECT r.id, r.receipt_date, COALESCE(CAST(r.total AS REAL), 0),
		        (SELECT COUNT(*) FROM line_items li WHERE li.receipt_id = r.id)
		 FROM receipts r
		 WHERE r.store_id = ? AND r.household_id = ?
		 ORDER BY r.receipt_date DESC
		 LIMIT 10`,
		storeID, householdID,
	)
	if err == nil {
		defer tripRows.Close()
		for tripRows.Next() {
			var trip storeRecentTrip
			var receiptDate time.Time
			if tripRows.Scan(&trip.ReceiptID, &receiptDate, &trip.Total, &trip.ItemCount) == nil {
				trip.Date = receiptDate.Format("2006-01-02")
				trip.Total = math.Round(trip.Total*100) / 100
				resp.RecentTrips = append(resp.RecentTrips, trip)
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
}

// Trips returns receipts as trip items for charting, with pagination.
// GET /api/v1/analytics/trips?limit=500&offset=0
func (h *AnalyticsHandler) Trips(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	if offset < 0 {
		offset = 0
	}

	rows, err := h.DB.Query(
		`SELECT r.id, r.receipt_date, r.store_id, COALESCE(s.name, 'Unknown') as store_name,
		        COALESCE(CAST(r.total AS REAL), 0),
		        (SELECT COUNT(*) FROM line_items li WHERE li.receipt_id = r.id)
		 FROM receipts r
		 LEFT JOIN stores s ON r.store_id = s.id
		 WHERE r.household_id = ?
		 ORDER BY r.receipt_date DESC
		 LIMIT ? OFFSET ?`,
		householdID, limit, offset,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	trips := make([]tripItem, 0)
	for rows.Next() {
		var t tripItem
		var receiptDate time.Time
		if err := rows.Scan(&t.ReceiptID, &receiptDate, &t.StoreID, &t.StoreName, &t.Total, &t.ItemCount); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		t.Date = receiptDate.Format("2006-01-02")
		t.Total = math.Round(t.Total*100) / 100
		trips = append(trips, t)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, trips)
}

// Deals returns products where latest price is significantly below average, with pagination.
// GET /api/v1/analytics/deals?limit=100&offset=0
func (h *AnalyticsHandler) Deals(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	if offset < 0 {
		offset = 0
	}

	rows, err := h.DB.Query(
		`SELECT p.id, p.name, s.name,
		        COALESCE(CAST(latest_pp.normalized_price AS REAL), CAST(latest_pp.unit_price AS REAL)) as latest_price,
		        stats.avg_price, latest_pp.is_sale
		 FROM products p
		 JOIN product_prices latest_pp ON latest_pp.product_id = p.id
		   AND latest_pp.receipt_date = (
		       SELECT MAX(pp2.receipt_date)
		       FROM product_prices pp2
		       WHERE pp2.product_id = p.id
		   )
		 JOIN stores s ON latest_pp.store_id = s.id
		 JOIN (
		     SELECT product_id,
		            AVG(COALESCE(CAST(normalized_price AS REAL), CAST(unit_price AS REAL))) as avg_price
		     FROM product_prices
		     GROUP BY product_id
		     HAVING COUNT(*) >= 2
		 ) stats ON stats.product_id = p.id
		 WHERE p.household_id = ? AND p.is_non_product = 0
		   AND COALESCE(CAST(latest_pp.normalized_price AS REAL), CAST(latest_pp.unit_price AS REAL)) < stats.avg_price * 0.85
		 ORDER BY (1.0 - COALESCE(CAST(latest_pp.normalized_price AS REAL), CAST(latest_pp.unit_price AS REAL)) / stats.avg_price) DESC
		 LIMIT ? OFFSET ?`,
		householdID, limit, offset,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	deals := make([]dealItem, 0)
	for rows.Next() {
		var d dealItem
		if err := rows.Scan(&d.ProductID, &d.ProductName, &d.Store, &d.LatestPrice, &d.AvgPrice, &d.IsSale); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		d.AvgPrice = math.Round(d.AvgPrice*100) / 100
		d.LatestPrice = math.Round(d.LatestPrice*100) / 100
		if d.AvgPrice > 0 {
			d.SavingsPercent = math.Round((1.0-d.LatestPrice/d.AvgPrice)*10000) / 100
		}
		deals = append(deals, d)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, deals)
}

// GroupTrend returns price history and stats for all products in a product group.
// GET /api/v1/analytics/product-groups/:id/trend
func (h *AnalyticsHandler) GroupTrend(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)
	groupID := c.Param("id")

	// Verify group belongs to household.
	var exists int
	err := h.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM product_groups WHERE id = ? AND household_id = ?",
		groupID, householdID,
	).Scan(&exists)
	if err != nil || exists == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "group not found"})
	}

	sixMonthsAgo := time.Now().UTC().AddDate(0, -6, 0).Format("2006-01-02")

	// Price history for all group members (last 6 months).
	rows, err := h.DB.Query(
		`SELECT pp.receipt_date,
		        COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price,
		        s.name, pp.is_sale
		 FROM product_prices pp
		 JOIN products p ON pp.product_id = p.id
		 JOIN stores s ON pp.store_id = s.id
		 WHERE p.product_group_id = ? AND pp.receipt_date >= ?
		 ORDER BY pp.receipt_date ASC`,
		groupID, sixMonthsAgo,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	history := make([]pricePoint, 0)
	for rows.Next() {
		var pp pricePoint
		var receiptDate time.Time
		if err := rows.Scan(&receiptDate, &pp.NormalizedPrice, &pp.Store, &pp.IsSale); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		pp.Date = receiptDate.Format("2006-01-02")
		history = append(history, pp)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	resp := productTrendResponse{
		PriceHistory: history,
	}

	// Percent change: first vs last in the window.
	if len(history) >= 2 {
		first := history[0].NormalizedPrice
		last := history[len(history)-1].NormalizedPrice
		if first > 0 {
			resp.PercentChange = math.Round(((last-first)/first)*10000) / 100
		}
	}

	// Min/max/avg with store names.
	h.DB.QueryRow(
		`SELECT COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price, s.name
		 FROM product_prices pp
		 JOIN products p ON pp.product_id = p.id
		 JOIN stores s ON pp.store_id = s.id
		 WHERE p.product_group_id = ? AND pp.receipt_date >= ?
		 ORDER BY price ASC LIMIT 1`,
		groupID, sixMonthsAgo,
	).Scan(&resp.MinPrice, &resp.MinStore)

	h.DB.QueryRow(
		`SELECT COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) as price, s.name
		 FROM product_prices pp
		 JOIN products p ON pp.product_id = p.id
		 JOIN stores s ON pp.store_id = s.id
		 WHERE p.product_group_id = ? AND pp.receipt_date >= ?
		 ORDER BY price DESC LIMIT 1`,
		groupID, sixMonthsAgo,
	).Scan(&resp.MaxPrice, &resp.MaxStore)

	h.DB.QueryRow(
		`SELECT AVG(COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)))
		 FROM product_prices pp
		 JOIN products p ON pp.product_id = p.id
		 WHERE p.product_group_id = ? AND pp.receipt_date >= ?`,
		groupID, sixMonthsAgo,
	).Scan(&resp.AvgPrice)
	resp.AvgPrice = math.Round(resp.AvgPrice*100) / 100

	return c.JSON(http.StatusOK, resp)
}

// BuyAgain returns products predicted to need repurchasing soon.
// Uses the quantity-aware interval algorithm from the plan.
// GET /api/v1/analytics/buy-again
func (h *AnalyticsHandler) BuyAgain(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	// last_price/last_store_name: join once to the latest product_prices row per
	// product (deterministic tiebreaker: receipt_date, created_at, id — same as
	// productListColumns in internal/api/products.go), then LEFT JOIN stores.
	rows, err := h.DB.Query(
		`SELECT sub.product_id, p.name,
		        AVG(sub.days_gap) / AVG(sub.quantity) as avg_days_per_unit,
		        sub.last_qty * (AVG(sub.days_gap) / AVG(sub.quantity)) as est_supply_days,
		        julianday('now') - julianday(sub.last_date) as days_since_last,
		        PRINTF('%.2f', latest.unit_price) as last_price,
		        s.name as last_store_name
		 FROM (
		     SELECT pp.product_id,
		            CAST(pp.quantity AS REAL) as quantity,
		            pp.receipt_date,
		            julianday(LEAD(pp.receipt_date) OVER w) - julianday(pp.receipt_date) as days_gap,
		            LAST_VALUE(pp.receipt_date) OVER (
		                PARTITION BY pp.product_id ORDER BY pp.receipt_date
		                ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
		            ) as last_date,
		            LAST_VALUE(CAST(pp.quantity AS REAL)) OVER (
		                PARTITION BY pp.product_id ORDER BY pp.receipt_date
		                ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
		            ) as last_qty
		     FROM product_prices pp
		     JOIN products p ON p.id = pp.product_id
		     WHERE p.household_id = ? AND p.is_non_product = 0
		     WINDOW w AS (PARTITION BY pp.product_id ORDER BY pp.receipt_date
		                  ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
		 ) sub
		 JOIN products p ON p.id = sub.product_id
		 LEFT JOIN (
		     SELECT pp2.product_id, pp2.unit_price, pp2.store_id,
		            ROW_NUMBER() OVER (PARTITION BY pp2.product_id
		                               ORDER BY pp2.receipt_date DESC, pp2.created_at DESC, pp2.id DESC) AS rn
		     FROM product_prices pp2
		 ) latest ON latest.product_id = sub.product_id AND latest.rn = 1
		 LEFT JOIN stores s ON s.id = latest.store_id
		 WHERE sub.days_gap IS NOT NULL
		 GROUP BY sub.product_id
		 HAVING COUNT(*) >= 2
		 ORDER BY (julianday('now') - julianday(sub.last_date)) /
		          (sub.last_qty * (AVG(sub.days_gap) / AVG(sub.quantity))) DESC`,
		householdID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	items := make([]buyAgainItem, 0)
	for rows.Next() {
		var item buyAgainItem
		if err := rows.Scan(&item.ProductID, &item.ProductName,
			&item.AvgDaysPerUnit, &item.EstSupplyDays, &item.DaysSinceLast,
			&item.LastPrice, &item.LastStoreName); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		item.AvgDaysPerUnit = math.Round(item.AvgDaysPerUnit*100) / 100
		item.EstSupplyDays = math.Round(item.EstSupplyDays*100) / 100
		item.DaysSinceLast = math.Round(item.DaysSinceLast*100) / 100

		if item.EstSupplyDays > 0 {
			item.UrgencyRatio = math.Round((item.DaysSinceLast/item.EstSupplyDays)*100) / 100
		}

		switch {
		case item.UrgencyRatio > 1.0:
			item.Urgency = "overdue"
		case item.UrgencyRatio > 0.8:
			item.Urgency = "low"
		case item.UrgencyRatio > 0.6:
			item.Urgency = "horizon"
		default:
			item.Urgency = "stocked"
		}

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, items)
}

// Rhythm returns shopping cadence stats for the authenticated household.
// GET /api/v1/analytics/rhythm
//
// Two 30-day windows are compared:
//   - current: [now-30d, tomorrow)  — upper bound is tomorrow (now.AddDate(0,0,1)),
//     so today's receipts are included
//   - prior:   [now-60d, now-30d)
func (h *AnalyticsHandler) Rhythm(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	// All date boundaries computed in Go; never use SQLite date('now',...).
	now := time.Now().UTC()
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	cur := now.AddDate(0, 0, -30).Format("2006-01-02")
	prev := now.AddDate(0, 0, -60).Format("2006-01-02")

	var resp rhythmResponse

	// --- trips current window ---
	const qTrips = `
		SELECT COUNT(*)
		FROM receipts r
		WHERE r.household_id = ?
		  AND r.receipt_date >= ?
		  AND r.receipt_date <  ?
		  AND r.status IN ('pending','matched','reviewed')`

	if err := h.DB.QueryRow(qTrips, householdID, cur, tomorrow).Scan(&resp.Trips.Current); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	// --- trips prior window ---
	if err := h.DB.QueryRow(qTrips, householdID, prev, cur).Scan(&resp.Trips.Prior); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	if resp.Trips.Prior > 0 {
		d := ((float64(resp.Trips.Current) - float64(resp.Trips.Prior)) / float64(resp.Trips.Prior)) * 100
		resp.Trips.DeltaPct = &d
	}

	// --- avg basket ---
	const qBasket = `
		SELECT COALESCE(AVG(CAST(r.total AS REAL)), 0)
		FROM receipts r
		WHERE r.household_id = ?
		  AND r.receipt_date >= ?
		  AND r.receipt_date <  ?
		  AND r.status IN ('pending','matched','reviewed')`

	if err := h.DB.QueryRow(qBasket, householdID, cur, tomorrow).Scan(&resp.AvgBasket.Current); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	if err := h.DB.QueryRow(qBasket, householdID, prev, cur).Scan(&resp.AvgBasket.Prior); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	resp.AvgBasket.Current = math.Round(resp.AvgBasket.Current*100) / 100
	resp.AvgBasket.Prior = math.Round(resp.AvgBasket.Prior*100) / 100

	// --- avg items per trip (current window) ---
	if err := h.DB.QueryRow(`
		SELECT COALESCE(CAST(COUNT(li.id) AS REAL) / NULLIF(COUNT(DISTINCT r.id), 0), 0)
		FROM receipts r
		LEFT JOIN line_items li ON li.receipt_id = r.id
		WHERE r.household_id = ?
		  AND r.receipt_date >= ?
		  AND r.receipt_date <  ?
		  AND r.status IN ('pending','matched','reviewed')`,
		householdID, cur, tomorrow,
	).Scan(&resp.AvgItemsPerTrip); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	resp.AvgItemsPerTrip = math.Round(resp.AvgItemsPerTrip*10) / 10

	// --- history days (all-time span) ---
	if err := h.DB.QueryRow(`
		SELECT CAST(julianday(?) - julianday(MIN(r.receipt_date)) AS INTEGER)
		FROM receipts r
		WHERE r.household_id = ?
		  AND r.status IN ('pending','matched','reviewed')`,
		tomorrow, householdID,
	).Scan(&resp.HistoryDays); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	// --- most-shopped DOW (all-time; only when >= 14 days of history) ---
	if resp.HistoryDays >= 14 {
		rows, err := h.DB.Query(`
			SELECT strftime('%w', r.receipt_date) AS dow, COUNT(*) AS cnt
			FROM receipts r
			WHERE r.household_id = ?
			  AND r.status IN ('pending','matched','reviewed')
			GROUP BY dow
			ORDER BY cnt DESC, dow ASC
			LIMIT 2`,
			householdID,
		)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
		defer rows.Close()

		type dowRow struct {
			dow string
			cnt int
		}
		var top []dowRow
		for rows.Next() {
			var r dowRow
			if err := rows.Scan(&r.dow, &r.cnt); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "database error")
			}
			top = append(top, r)
		}
		if err := rows.Err(); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}

		dowNames := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

		switch len(top) {
		case 1:
			idx, _ := strconv.Atoi(top[0].dow)
			s := dowNames[idx]
			resp.MostShoppedDOW = &s
		case 2:
			if top[0].cnt == top[1].cnt {
				// Exact two-way tie — return "Day1/Day2" (ORDER BY dow ASC gives lower index first)
				idx0, _ := strconv.Atoi(top[0].dow)
				idx1, _ := strconv.Atoi(top[1].dow)
				s := dowNames[idx0] + "/" + dowNames[idx1]
				resp.MostShoppedDOW = &s
			} else {
				idx, _ := strconv.Atoi(top[0].dow)
				s := dowNames[idx]
				resp.MostShoppedDOW = &s
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
}

// --- Category Breakdown types ---

type categoryBucket struct {
	Name       string   `json:"name"`
	Current    float64  `json:"current"`
	Prior      float64  `json:"prior"`
	PctOfTotal float64  `json:"pct_of_total"`
	DeltaPct   *float64 `json:"delta_pct"`
}

type categoryBreakdownResponse struct {
	WindowDays int              `json:"window_days"`
	Total      float64          `json:"total"`
	Categories []categoryBucket `json:"categories"`
}

// qCategoryBreakdown is the SQL for one window of category spending.
// Placeholders: householdID, start (inclusive), end (exclusive).
const qCategoryBreakdown = `
SELECT
  CASE
    WHEN li.product_id IS NULL                         THEN 'Unmatched'
    WHEN COALESCE(NULLIF(p.category, ''), NULL) IS NULL THEN 'Uncategorized'
    ELSE p.category
  END AS bucket,
  SUM(CAST(li.total_price AS REAL)) AS amount
FROM line_items li
JOIN receipts r ON r.id = li.receipt_id
LEFT JOIN products p ON p.id = li.product_id
WHERE r.household_id = ?
  AND r.status IN ('pending', 'matched', 'reviewed')
  AND r.receipt_date >= ?
  AND r.receipt_date < ?
GROUP BY bucket
ORDER BY amount DESC`

// CategoryBreakdown returns category spending for the last 30 days vs prior 30 days.
// GET /api/v1/analytics/category-breakdown
//
// Two 30-day windows are compared:
//   - current: [now-30d, tomorrow)  — upper bound is tomorrow (now.AddDate(0,0,1)),
//     so today's receipts are included
//   - prior:   [now-60d, now-30d)
//
// Buckets are derived from line_items (not product_prices, so unmatched items are
// never silently dropped):
//   - real category name — matched item with a non-empty products.category
//   - "Uncategorized"   — matched item whose products.category is NULL or ""
//   - "Unmatched"       — line_items.product_id IS NULL (no product match at all)
func (h *AnalyticsHandler) CategoryBreakdown(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	now := time.Now().UTC()
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	cur := now.AddDate(0, 0, -30).Format("2006-01-02")
	prev := now.AddDate(0, 0, -60).Format("2006-01-02")

	// runWindow executes the category query for [start, end) and returns a map
	// of bucket name → amount.
	runWindow := func(start, end string) (map[string]float64, error) {
		rows, err := h.DB.Query(qCategoryBreakdown, householdID, start, end)
		if err != nil {
			return nil, fmt.Errorf("category query: %w", err)
		}
		defer rows.Close()
		result := make(map[string]float64)
		for rows.Next() {
			var bucket string
			var amount float64
			if err := rows.Scan(&bucket, &amount); err != nil {
				return nil, fmt.Errorf("category scan: %w", err)
			}
			result[bucket] = amount
		}
		return result, rows.Err()
	}

	currentMap, err := runWindow(cur, tomorrow)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	priorMap, err := runWindow(prev, cur)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	seen := make(map[string]struct{})
	for k := range currentMap {
		seen[k] = struct{}{}
	}
	for k := range priorMap {
		seen[k] = struct{}{}
	}

	var total float64
	for _, v := range currentMap {
		total += v
	}
	total = math.Round(total*100) / 100

	type kv struct {
		name    string
		current float64
		prior   float64
	}
	kvs := make([]kv, 0, len(seen))
	for name := range seen {
		kvs = append(kvs, kv{
			name:    name,
			current: currentMap[name],
			prior:   priorMap[name],
		})
	}
	for i := 0; i < len(kvs); i++ {
		for j := i + 1; j < len(kvs); j++ {
			if kvs[j].current > kvs[i].current ||
				(kvs[j].current == kvs[i].current && kvs[j].prior > kvs[i].prior) {
				kvs[i], kvs[j] = kvs[j], kvs[i]
			}
		}
	}

	categories := make([]categoryBucket, 0, len(kvs))
	for _, entry := range kvs {
		curAmt := math.Round(entry.current*100) / 100
		priorAmt := math.Round(entry.prior*100) / 100

		var pct float64
		if total > 0 {
			pct = math.Round((curAmt/total)*10000) / 100
		}

		var deltaPtr *float64
		if priorAmt > 0 {
			delta := math.Round(((curAmt-priorAmt)/priorAmt)*10000) / 100
			deltaPtr = &delta
		}

		categories = append(categories, categoryBucket{
			Name:       entry.name,
			Current:    curAmt,
			Prior:      priorAmt,
			PctOfTotal: pct,
			DeltaPct:   deltaPtr,
		})
	}

	return c.JSON(http.StatusOK, categoryBreakdownResponse{
		WindowDays: 30,
		Total:      total,
		Categories: categories,
	})
}

// Savings returns total discount_amount across three time windows: month-to-date,
// last 30 days, and year-to-date. All windows use `tomorrow` as the exclusive upper
// bound (to include today's receipts). discount_amount is stored positive and summed
// without negation.
// GET /api/v1/analytics/savings
func (h *AnalyticsHandler) Savings(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	now := time.Now().UTC()
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	last30Start := now.AddDate(0, 0, -30).Format("2006-01-02")

	// runQuery executes the savings query for [start, end) and returns the total.
	runQuery := func(start, end string) (float64, error) {
		var amount float64
		err := h.DB.QueryRow(qSavings, householdID, start, end).Scan(&amount)
		if err != nil {
			return 0, fmt.Errorf("savings query: %w", err)
		}
		return math.Round(amount*100) / 100, nil
	}

	monthToDate, err := runQuery(monthStart, tomorrow)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	last30D, err := runQuery(last30Start, tomorrow)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	yearToDate, err := runQuery(yearStart, tomorrow)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	return c.JSON(http.StatusOK, savingsResponse{
		MonthToDate: monthToDate,
		Last30D:     last30D,
		YearToDate:  yearToDate,
	})
}

// qSavings is the SQL for querying total discounts in a date window.
// Placeholders: householdID, start (inclusive), end (exclusive).
const qSavings = `
SELECT COALESCE(SUM(CAST(li.discount_amount AS REAL)), 0)
FROM line_items li
JOIN receipts r ON r.id = li.receipt_id
WHERE r.household_id = ?
  AND r.status IN ('pending', 'matched', 'reviewed')
  AND r.receipt_date >= ?
  AND r.receipt_date < ?`

// --- Staples types ---

// stapleItem is a single product row in the /analytics/staples response.
// Projection fields (weekly/monthly/yearly) are nullable to signal "not
// enough history" or "stale data" — handler sets them to nil when the
// household-wide span is under 60 days or the latest receipt is older
// than 45 days.
type stapleItem struct {
	ProductID        string    `json:"product_id"`
	Name             string    `json:"name"`
	Category         string    `json:"category"`
	TimesBought      int       `json:"times_bought"`
	CadenceDays      float64   `json:"cadence_days"`
	TotalSpent       float64   `json:"total_spent"`
	AvgPrice         float64   `json:"avg_price"`
	WeeklySpend      *float64  `json:"weekly_spend"`
	MonthlySpend     *float64  `json:"monthly_spend"`
	YearlyProjection *float64  `json:"yearly_projection"`
	SparklinePoints  []float64 `json:"sparkline_points"`
}

// qStaplesList is the main staples CTE. Rules:
//   - times_bought uses COUNT(DISTINCT pp.receipt_id) — invariant 2 (a single
//     product split across multiple line items on one receipt counts once).
//   - distinct_dates uses COUNT(DISTINCT date(pp.receipt_date)) — invariant 1
//     (guards against same-day re-scans inflating the denominator).
//   - Cadence must be <=60d (calendar-event cadence; Staples does NOT use
//     Buy-Again's AVG(days_gap)/AVG(quantity) unit-rate formula).
//   - total_spent = SUM(unit_price * quantity) — avoids NULL normalized_price
//     on spreadsheet imports.
//   - Sort: times_bought DESC, total_spent DESC, product_id ASC (deterministic
//     tiebreak so tests and pagination are stable).
//
// Placeholder: householdID.
const qStaplesList = `
WITH per_product AS (
  SELECT
    pp.product_id,
    COUNT(DISTINCT pp.receipt_id)                                        AS times_bought,
    COUNT(DISTINCT date(pp.receipt_date))                                AS distinct_dates,
    julianday(MAX(pp.receipt_date)) - julianday(MIN(pp.receipt_date))    AS span_days,
    SUM(CAST(pp.unit_price AS REAL) * CAST(pp.quantity AS REAL))         AS total_spent
  FROM product_prices pp
  JOIN products p ON p.id = pp.product_id
  JOIN receipts r ON r.id = pp.receipt_id
  WHERE p.household_id = ?
    AND p.is_non_product = 0
    AND r.status IN ('pending', 'matched', 'reviewed')
  GROUP BY pp.product_id
  HAVING distinct_dates >= 2
     AND (span_days / (distinct_dates - 1)) <= 60
)
SELECT
  pp.product_id,
  p.name,
  COALESCE(p.category, '')                           AS category,
  pp.times_bought,
  CAST(pp.span_days AS REAL) / (pp.distinct_dates - 1) AS cadence_days,
  pp.total_spent,
  pp.span_days
FROM per_product pp
JOIN products p ON p.id = pp.product_id
ORDER BY pp.times_bought DESC, pp.total_spent DESC, pp.product_id ASC`

// qStaplesSpan returns the household-wide receipt span and staleness in a
// single scalar query. Placeholders: today (YYYY-MM-DD), householdID.
const qStaplesSpan = `
SELECT
  COALESCE(julianday(MAX(r.receipt_date)) - julianday(MIN(r.receipt_date)), 0)   AS household_span_days,
  COALESCE(julianday(?) - julianday(MAX(r.receipt_date)), 999999)                AS days_since_last_receipt
FROM receipts r
WHERE r.household_id = ?
  AND r.status IN ('pending', 'matched', 'reviewed')`

// qStaplesSparkline batches sparkline points for every household product.
// We drop the per_product filter from SQL (it would require dynamic placeholder
// juggling) and instead filter in Go via a product_id -> staple map.
// Placeholder: householdID. Returns ASC-by-date rows of the last 8 purchases
// per product.
const qStaplesSparkline = `
SELECT product_id, unit_price, receipt_date
FROM (
  SELECT pp.product_id,
         CAST(pp.unit_price AS REAL) AS unit_price,
         pp.receipt_date,
         ROW_NUMBER() OVER (
           PARTITION BY pp.product_id
           ORDER BY pp.receipt_date DESC, pp.created_at DESC, pp.id DESC
         ) AS rn
  FROM product_prices pp
  JOIN products p ON p.id = pp.product_id
  JOIN receipts r ON r.id = pp.receipt_id
  WHERE p.household_id = ?
    AND p.is_non_product = 0
    AND r.status IN ('pending', 'matched', 'reviewed')
) ranked
WHERE rn <= 8
ORDER BY product_id, receipt_date ASC`

// --- Price Moves types ---

// priceMoveItem describes one product whose unit price has shifted between the
// 0-30 day window (recent) and the 31-90 day window (prior).
type priceMoveItem struct {
	ProductID          string  `json:"product_id"`
	Name               string  `json:"name"`
	Avg030D            float64 `json:"avg_0_30d"`
	Avg3090D           float64 `json:"avg_30_90d"`
	PctChange          float64 `json:"pct_change"`
	Unit               string  `json:"unit"`
	ObservationsRecent int     `json:"observations_recent"`
	ObservationsPrior  int     `json:"observations_prior"`
}

// priceMovesResponse is the JSON envelope returned by /analytics/price-moves.
type priceMovesResponse struct {
	Up   []priceMoveItem `json:"up"`
	Down []priceMoveItem `json:"down"`
}

// qPriceMoves is the SQL for computing average unit price per product in two
// rolling windows. All date boundaries are passed as ? parameters — plan
// Invariant: date windows are computed in Go (time.Now().UTC()), never via
// SQLite DATE('now', ...). WHERE adds r.status IN ('pending','matched','reviewed')
// and p.is_non_product = 0 per plan Invariants 3 and 5. HAVING requires
// COUNT(DISTINCT DATE(receipt_date)) >= 3 for a meaningful price trajectory.
// Parameter order: cutoff30 (x7 SELECT), householdID, cutoff90, today,
// cutoff30 (x7 HAVING).
const qPriceMoves = `
SELECT
    p.id            AS product_id,
    p.name,
    pp.unit,
    AVG(CASE WHEN pp.receipt_date >= ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END) AS avg_030d,
    AVG(CASE WHEN pp.receipt_date <  ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END) AS avg_3090d,
    ROUND(
        (AVG(CASE WHEN pp.receipt_date >= ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END) -
         AVG(CASE WHEN pp.receipt_date <  ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END)) /
        AVG(CASE WHEN pp.receipt_date <  ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END) * 100
    , 2) AS pct_change,
    COUNT(CASE WHEN pp.receipt_date >= ? THEN 1 END) AS observations_recent,
    COUNT(CASE WHEN pp.receipt_date <  ? THEN 1 END) AS observations_prior
FROM product_prices pp
JOIN receipts r ON r.id = pp.receipt_id
JOIN products p  ON p.id = pp.product_id
WHERE
    r.household_id = ?
    AND pp.receipt_date >= ?
    AND pp.receipt_date <  ?
    AND COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) > 0
    AND r.status IN ('pending', 'matched', 'reviewed')
    AND p.is_non_product = 0
GROUP BY p.id, p.name, pp.unit
HAVING
    COUNT(DISTINCT DATE(pp.receipt_date)) >= 3
    AND COUNT(CASE WHEN pp.receipt_date >= ? THEN 1 END) >= 1
    AND COUNT(CASE WHEN pp.receipt_date <  ? THEN 1 END) >= 1
    AND AVG(CASE WHEN pp.receipt_date <  ? THEN COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) END) > 0
    AND COUNT(DISTINCT CASE WHEN pp.receipt_date >= ? THEN pp.unit END) = 1
    AND COUNT(DISTINCT CASE WHEN pp.receipt_date <  ? THEN pp.unit END) = 1
    AND MAX(CASE WHEN pp.receipt_date >= ? THEN pp.unit END)
        = MAX(CASE WHEN pp.receipt_date <  ? THEN pp.unit END)
ORDER BY observations_recent DESC, p.id ASC`

// PriceMoves returns products whose normalized unit price has shifted by >=10%
// between the last 30 days (recent window) and the 31–90 day window (prior).
// Only products whose unit is identical across both windows are compared, ensuring
// apples-to-apples price comparison (e.g. "$/lb" vs "$/ea" are never mixed).
// Requires >=3 distinct dates in the 90-day window for signal confidence.
// Results are split into "up" and "down" slices, each sorted by |pct_change|
// descending and capped at 5 items per direction.
// GET /api/v1/analytics/price-moves
func (h *AnalyticsHandler) PriceMoves(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)

	now := time.Now().UTC()
	today := now.AddDate(0, 0, 1).Format("2006-01-02")
	cutoff30 := now.AddDate(0, 0, -30).Format("2006-01-02")
	cutoff90 := now.AddDate(0, 0, -90).Format("2006-01-02")

	rows, err := h.DB.QueryContext(ctx, qPriceMoves,
		cutoff30, cutoff30, cutoff30, cutoff30, cutoff30, cutoff30, cutoff30,
		householdID, cutoff90, today,
		cutoff30, cutoff30, cutoff30, cutoff30, cutoff30, cutoff30, cutoff30,
	)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	defer rows.Close()

	all := make([]priceMoveItem, 0)
	for rows.Next() {
		var item priceMoveItem
		if err := rows.Scan(
			&item.ProductID,
			&item.Name,
			&item.Unit,
			&item.Avg030D,
			&item.Avg3090D,
			&item.PctChange,
			&item.ObservationsRecent,
			&item.ObservationsPrior,
		); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
		// Round money fields to 2 decimal places at the Go layer.
		item.Avg030D = math.Round(item.Avg030D*100) / 100
		item.Avg3090D = math.Round(item.Avg3090D*100) / 100
		item.PctChange = math.Round(item.PctChange*100) / 100
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	up := make([]priceMoveItem, 0, 5)
	down := make([]priceMoveItem, 0, 5)

	for _, item := range all {
		if item.PctChange > 0 {
			up = append(up, item)
		} else if item.PctChange < 0 {
			down = append(down, item)
		}
		// pct_change == 0 is excluded (no meaningful move)
	}

	// Sort each group by |pct_change| DESC (tiebreaker is already observations_recent DESC, p.id ASC from SQL).
	sortByAbsPct := func(s []priceMoveItem) {
		for i := 0; i < len(s); i++ {
			for j := i + 1; j < len(s); j++ {
				if math.Abs(s[j].PctChange) > math.Abs(s[i].PctChange) {
					s[i], s[j] = s[j], s[i]
				}
			}
		}
	}
	sortByAbsPct(up)
	sortByAbsPct(down)

	// Cap at top 5.
	if len(up) > 5 {
		up = up[:5]
	}
	if len(down) > 5 {
		down = down[:5]
	}

	return c.JSON(http.StatusOK, priceMovesResponse{Up: up, Down: down})
}

// Staples returns household staple products — items purchased on >=2 distinct
// calendar dates with an average inter-purchase cadence <=60 days (date-based,
// not unit-rate like BuyAgain). Spend projections (weekly/monthly/yearly) are
// gated on >=60d household history + last receipt within 45 days; otherwise null.
// Each product's sparkline carries at most 8 price points.
// GET /api/v1/analytics/staples
func (h *AnalyticsHandler) Staples(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	// Single UTC "now" reused across all queries and projections.
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// --- SQL-B: household span + staleness ---
	var householdSpanDays, daysSinceLast float64
	if err := h.DB.QueryRow(qStaplesSpan, today, householdID).Scan(&householdSpanDays, &daysSinceLast); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	projectionsValid := householdSpanDays >= 60 && daysSinceLast < 45

	// --- SQL-A: main staples list ---
	rows, err := h.DB.Query(qStaplesList, householdID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	defer rows.Close()

	staples := make([]stapleItem, 0)
	idx := make(map[string]int) // product_id -> index in staples

	for rows.Next() {
		var it stapleItem
		var spanDays float64
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Category,
			&it.TimesBought, &it.CadenceDays, &it.TotalSpent, &spanDays); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}

		// Rounding at JSON boundary.
		it.CadenceDays = math.Round(it.CadenceDays*10) / 10
		it.TotalSpent = math.Round(it.TotalSpent*100) / 100
		if it.TimesBought > 0 {
			it.AvgPrice = math.Round((it.TotalSpent/float64(it.TimesBought))*100) / 100
		}

		// Per-product projection gate: also require spanDays > 0 to avoid
		// divide-by-zero (a product with all purchases on the same day would
		// already be excluded by distinct_dates >= 2, but belt-and-suspenders).
		if projectionsValid && spanDays > 0 {
			weekly := it.TotalSpent / (spanDays / 7.0)
			monthly := weekly * 4.345
			yearly := monthly * 12
			w := math.Round(weekly*100) / 100
			m := math.Round(monthly*100) / 100
			y := math.Round(yearly*100) / 100
			it.WeeklySpend = &w
			it.MonthlySpend = &m
			it.YearlyProjection = &y
		}

		it.SparklinePoints = make([]float64, 0, 8)
		idx[it.ProductID] = len(staples)
		staples = append(staples, it)
	}
	if err := rows.Err(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	// --- SQL-C: sparkline batch (only groups products that made the list) ---
	if len(staples) > 0 {
		sparkRows, err := h.DB.Query(qStaplesSparkline, householdID)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
		defer sparkRows.Close()
		for sparkRows.Next() {
			var productID string
			var unitPrice float64
			var receiptDate time.Time
			if err := sparkRows.Scan(&productID, &unitPrice, &receiptDate); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "database error")
			}
			if i, ok := idx[productID]; ok {
				staples[i].SparklinePoints = append(staples[i].SparklinePoints, math.Round(unitPrice*100)/100)
			}
		}
		if err := sparkRows.Err(); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
	}

	return c.JSON(http.StatusOK, staples)
}

// --- Inflation types ---

// inflationResponse is the JSON shape for GET /analytics/inflation.
// Change3moPct and Change6moPct are nil when there is insufficient history or
// basket overlap. Suppressed=true (with a reason) when BOTH are nil.
//
// The index is Laspeyres-style: I(W) = Σ_p qty_p × avg_price_p(W), where
// qty_p is the per-product quantity median computed from the last 90 days of
// purchases (falling back to all-time when < 2 observations in the 90-day
// window). Symmetric exclusion ensures a product only contributes to a
// comparison if it has observations in BOTH windows of the pair.
type inflationResponse struct {
	Change3moPct      *float64 `json:"change_3mo_pct"`
	Change6moPct      *float64 `json:"change_6mo_pct"`
	BasketSize        int      `json:"basket_size"`
	Suppressed        bool     `json:"suppressed"`
	SuppressionReason *string  `json:"suppression_reason"`
}

// inflationObs is a single price/quantity observation row from SQL-C.
type inflationObs struct {
	productID string
	price     float64
	qty       float64
	date      time.Time
}

// qInflationSpan returns household receipt span (days) and receipt count.
// Placeholders: householdID.
const qInflationSpan = `
SELECT
  COALESCE(julianday(MAX(r.receipt_date)) - julianday(MIN(r.receipt_date)), 0) AS span_days,
  COUNT(*)                                                                       AS receipt_count
FROM receipts r
WHERE r.household_id = ?
  AND r.status IN ('pending', 'matched', 'reviewed')`

// qInflationPrices fetches price + qty for all basket products over the last
// 210 days. Buckets are computed in Go.
// Placeholders: householdID, cutoff210 (YYYY-MM-DD), tomorrow (YYYY-MM-DD).
const qInflationPrices = `
SELECT
  pp.product_id,
  pp.receipt_date,
  COALESCE(CAST(pp.normalized_price AS REAL), CAST(pp.unit_price AS REAL)) AS price,
  CAST(pp.quantity AS REAL)                                                 AS qty
FROM product_prices pp
JOIN products p ON p.id = pp.product_id
JOIN receipts r ON r.id = pp.receipt_id
WHERE p.household_id = ?
  AND p.is_non_product = 0
  AND r.status IN ('pending', 'matched', 'reviewed')
  AND pp.receipt_date >= ?
  AND pp.receipt_date < ?
ORDER BY pp.product_id, pp.receipt_date ASC`

// median returns the median of a sorted (ascending) float64 slice.
// Caller must sort before calling. Returns 0 for empty slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Inflation returns a Laspeyres-style personal inflation index comparing the
// household's staple basket at current prices vs 3-month and 6-month prior windows.
// Basket weights are fixed at current-period median quantities (Laspeyres fixed-weight
// approach), so price changes drive the index, not quantity shifts. Symmetric exclusion
// means a product only enters a comparison if it has observed prices in both windows of
// the pair. The index is suppressed (both deltas nil) when history < 90/180 days or
// basket overlap falls below 50%.
// GET /api/v1/analytics/inflation
func (h *AnalyticsHandler) Inflation(c echo.Context) error {
	ctx := c.Request().Context()
	householdID := auth.HouseholdIDFrom(c)

	now := time.Now().UTC()
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	cutoff30 := now.AddDate(0, 0, -30).Format("2006-01-02")
	cutoff90 := now.AddDate(0, 0, -90).Format("2006-01-02")
	cutoff120 := now.AddDate(0, 0, -120).Format("2006-01-02")
	cutoff180 := now.AddDate(0, 0, -180).Format("2006-01-02")
	cutoff210 := now.AddDate(0, 0, -210).Format("2006-01-02")

	suppressed := func(reason string) error {
		r := reason
		return c.JSON(http.StatusOK, inflationResponse{
			BasketSize:        0,
			Suppressed:        true,
			SuppressionReason: &r,
		})
	}

	// --- SQL-A: basket via qStaplesList ---
	basketRows, err := h.DB.QueryContext(ctx, qStaplesList, householdID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	defer basketRows.Close()

	basketSet := make(map[string]struct{})
	for basketRows.Next() {
		var productID, name, category string
		var timesBought int
		var cadenceDays, totalSpent, spanDays float64
		if err := basketRows.Scan(&productID, &name, &category, &timesBought, &cadenceDays, &totalSpent, &spanDays); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
		basketSet[productID] = struct{}{}
	}
	if err := basketRows.Err(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	basketSize := len(basketSet)

	// --- SQL-B: household span + receipt count ---
	var spanDays float64
	var receiptCount int
	if err := h.DB.QueryRowContext(ctx, qInflationSpan, householdID).Scan(&spanDays, &receiptCount); err != nil && err != sql.ErrNoRows {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	if basketSize == 0 || receiptCount == 0 {
		return suppressed("Not enough overlap yet.")
	}

	// --- SQL-C: batched price+qty fetch over last 210d for basket products ---
	priceRows, err := h.DB.QueryContext(ctx, qInflationPrices, householdID, cutoff210, tomorrow)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}
	defer priceRows.Close()

	// Group observations by product_id; filter to basket.
	type productData struct {
		obs []inflationObs
	}
	byProduct := make(map[string]*productData)
	for id := range basketSet {
		byProduct[id] = &productData{}
	}

	for priceRows.Next() {
		var o inflationObs
		var receiptDate string
		if err := priceRows.Scan(&o.productID, &receiptDate, &o.price, &o.qty); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "database error")
		}
		t, err := time.Parse("2006-01-02", receiptDate)
		if err != nil {
			// Try full timestamp format as fallback.
			t, err = time.Parse("2006-01-02T15:04:05Z", receiptDate)
			if err != nil {
				continue
			}
		}
		o.date = t
		if pd, ok := byProduct[o.productID]; ok {
			pd.obs = append(pd.obs, o)
		}
	}
	if err := priceRows.Err(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	// Parse window boundaries as time.Time for comparison.
	cutoff30T, _ := time.Parse("2006-01-02", cutoff30)
	cutoff90T, _ := time.Parse("2006-01-02", cutoff90)
	cutoff120T, _ := time.Parse("2006-01-02", cutoff120)
	cutoff180T, _ := time.Parse("2006-01-02", cutoff180)
	cutoff210T, _ := time.Parse("2006-01-02", cutoff210)
	tomorrowT, _ := time.Parse("2006-01-02", tomorrow)

	// For each product, compute:
	//   qty_p  — median quantity from Q_90d [now-90, now); fallback to all-time if < 2 obs.
	//   avg price in W_current [now-30, now), W_3mo [now-120, now-90), W_6mo [now-210, now-180).
	type productStats struct {
		qty      float64
		avgCur   *float64 // W_current avg price
		avg3mo   *float64 // W_3mo avg price
		avg6mo   *float64 // W_6mo avg price
	}

	avgOf := func(obs []inflationObs, from, to time.Time) *float64 {
		var sum float64
		var n int
		for _, o := range obs {
			if !o.date.Before(from) && o.date.Before(to) {
				sum += o.price
				n++
			}
		}
		if n == 0 {
			return nil
		}
		v := sum / float64(n)
		return &v
	}

	stats := make(map[string]productStats, basketSize)
	for id, pd := range byProduct {
		// Compute qty_p from Q_90d: [cutoff90, tomorrow)
		var qtyQ90 []float64
		for _, o := range pd.obs {
			if !o.date.Before(cutoff90T) && o.date.Before(tomorrowT) {
				qtyQ90 = append(qtyQ90, o.qty)
			}
		}
		var qtySrc []float64
		if len(qtyQ90) >= 2 {
			qtySrc = qtyQ90
		} else {
			// Fallback to all-time (all rows in SQL-C 210d window).
			for _, o := range pd.obs {
				qtySrc = append(qtySrc, o.qty)
			}
		}
		if len(qtySrc) == 0 {
			continue // skip products with zero observations
		}
		sort.Float64s(qtySrc)
		qtyP := median(qtySrc)

		avgCur := avgOf(pd.obs, cutoff30T, tomorrowT)
		avg3mo := avgOf(pd.obs, cutoff120T, cutoff90T)
		avg6mo := avgOf(pd.obs, cutoff210T, cutoff180T)

		stats[id] = productStats{qty: qtyP, avgCur: avgCur, avg3mo: avg3mo, avg6mo: avg6mo}
	}

	// Compute Laspeyres index for 3-month comparison (W_current vs W_3mo).
	var iCur3, i3mo float64
	var overlap3 int
	for _, s := range stats {
		if s.avgCur == nil || s.avg3mo == nil {
			continue
		}
		iCur3 += s.qty * (*s.avgCur)
		i3mo += s.qty * (*s.avg3mo)
		overlap3++
	}

	// Compute Laspeyres index for 6-month comparison (W_current vs W_6mo).
	var iCur6, i6mo float64
	var overlap6 int
	for _, s := range stats {
		if s.avgCur == nil || s.avg6mo == nil {
			continue
		}
		iCur6 += s.qty * (*s.avgCur)
		i6mo += s.qty * (*s.avg6mo)
		overlap6++
	}

	// Apply suppression gates.
	var change3mo *float64
	var change6mo *float64

	if spanDays >= 90 && float64(overlap3)/float64(basketSize) >= 0.5 && i3mo > 0 {
		v := math.Round((iCur3/i3mo-1)*100*100) / 100
		change3mo = &v
	}
	if spanDays >= 180 && float64(overlap6)/float64(basketSize) >= 0.5 && i6mo > 0 {
		v := math.Round((iCur6/i6mo-1)*100*100) / 100
		change6mo = &v
	}

	isSuppressed := change3mo == nil && change6mo == nil
	var suppressionReason *string
	if isSuppressed {
		r := "Not enough overlap yet."
		suppressionReason = &r
	}

	return c.JSON(http.StatusOK, inflationResponse{
		Change3moPct:      change3mo,
		Change6moPct:      change6mo,
		BasketSize:        basketSize,
		Suppressed:        isSuppressed,
		SuppressionReason: suppressionReason,
	})
}
