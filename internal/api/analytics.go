package api

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
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
	analytics.GET("/product-groups/:id/trend", h.GroupTrend)
}

// --- Response types ---

type overviewResponse struct {
	TotalSpentThisMonth  float64 `json:"total_spent_this_month"`
	TotalSpentLastMonth  float64 `json:"total_spent_last_month"`
	MonthOverMonthChange float64 `json:"month_over_month_change"`
	TripCountThisMonth   int     `json:"trip_count_this_month"`
	AvgTripCost          float64 `json:"avg_trip_cost"`
	UniqueProducts       int     `json:"unique_products_purchased"`
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
	).Scan(&resp.TotalSpentThisMonth, &resp.TripCountThisMonth)

	// Total spent last month.
	h.DB.QueryRow(
		`SELECT COALESCE(SUM(CAST(total AS REAL)), 0)
		 FROM receipts
		 WHERE household_id = ? AND receipt_date >= ? AND receipt_date < ?`,
		householdID, lastMonthStart, thisMonthStart,
	).Scan(&resp.TotalSpentLastMonth)

	// Month-over-month change %.
	if resp.TotalSpentLastMonth > 0 {
		resp.MonthOverMonthChange = math.Round(((resp.TotalSpentThisMonth-resp.TotalSpentLastMonth)/resp.TotalSpentLastMonth)*10000) / 100
	}

	// Avg trip cost this month.
	if resp.TripCountThisMonth > 0 {
		resp.AvgTripCost = math.Round(resp.TotalSpentThisMonth/float64(resp.TripCountThisMonth)*100) / 100
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
		 WHERE pp.store_id = ? AND p.household_id = ?
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
		`SELECT r.id, r.receipt_date, COALESCE(s.name, 'Unknown') as store_name,
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
		if err := rows.Scan(&t.ReceiptID, &receiptDate, &t.StoreName, &t.Total, &t.ItemCount); err != nil {
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
		 WHERE p.household_id = ?
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

	rows, err := h.DB.Query(
		`SELECT sub.product_id, p.name,
		        AVG(sub.days_gap) / AVG(sub.quantity) as avg_days_per_unit,
		        sub.last_qty * (AVG(sub.days_gap) / AVG(sub.quantity)) as est_supply_days,
		        julianday('now') - julianday(sub.last_date) as days_since_last
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
		     WHERE p.household_id = ?
		     WINDOW w AS (PARTITION BY pp.product_id ORDER BY pp.receipt_date
		                  ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
		 ) sub
		 JOIN products p ON p.id = sub.product_id
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
			&item.AvgDaysPerUnit, &item.EstSupplyDays, &item.DaysSinceLast); err != nil {
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
