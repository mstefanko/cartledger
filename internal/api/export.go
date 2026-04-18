package api

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shopspring/decimal"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// ExportHandler holds dependencies for export/share endpoints.
type ExportHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// RegisterRoutes mounts export endpoints onto the protected group.
func (h *ExportHandler) RegisterRoutes(protected *echo.Group) {
	protected.GET("/lists/:id/share", h.ShareList)
	protected.GET("/export/receipts", h.ExportReceipts)
}

// ShareList returns a plain-text formatted shopping list for sharing.
// GET /api/v1/lists/:id/share
// Optional query param: ?store_id=<id> — scope the output to items assigned
// to that store. The store is validated against the household. When present,
// the header becomes "<list name> — <store name> (<N items>)".
func (h *ExportHandler) ShareList(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	listID := c.Param("id")
	storeID := c.QueryParam("store_id")

	// Get list name.
	var listName string
	err := h.DB.QueryRow(
		"SELECT name FROM shopping_lists WHERE id = ? AND household_id = ?",
		listID, householdID,
	).Scan(&listName)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "list not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Validate store_id belongs to household (when provided) and fetch its name.
	var storeName string
	if storeID != "" {
		err := h.DB.QueryRow(
			"SELECT COALESCE(NULLIF(nickname, ''), name) FROM stores WHERE id = ? AND household_id = ?",
			storeID, householdID,
		).Scan(&storeName)
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "store not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
	}

	// Get items with estimated prices. When storeID is set, scope to items
	// assigned to that store.
	query := `SELECT sli.name, sli.quantity, sli.unit, sli.checked,
		        COALESCE(
		          (SELECT pp.unit_price FROM product_prices pp
		           WHERE pp.product_id = sli.product_id
		           ORDER BY pp.receipt_date DESC LIMIT 1),
		          (SELECT MIN(pp.unit_price) FROM product_prices pp
		           JOIN products p ON p.id = pp.product_id
		           WHERE p.product_group_id = sli.product_group_id
		             AND pp.receipt_date = (
		                 SELECT MAX(pp2.receipt_date) FROM product_prices pp2
		                 WHERE pp2.product_id = pp.product_id AND pp2.store_id = pp.store_id))
		        ) AS estimated_price
		 FROM shopping_list_items sli
		 WHERE sli.list_id = ?`
	args := []interface{}{listID}
	if storeID != "" {
		query += " AND sli.assigned_store_id = ?"
		args = append(args, storeID)
	}
	query += " ORDER BY sli.sort_order, sli.created_at"

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	var lines []string
	var totalEstimate float64
	hasEstimate := false
	itemCount := 0

	for rows.Next() {
		var name, quantity string
		var unit *string
		var checked bool
		var price *float64

		if err := rows.Scan(&name, &quantity, &unit, &checked, &price); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}

		checkbox := "\u2610" // ☐
		if checked {
			checkbox = "\u2611" // ☑
		}

		// Build quantity/unit suffix.
		qtyStr := ""
		if quantity != "" && quantity != "1" {
			if unit != nil && *unit != "" {
				qtyStr = fmt.Sprintf(" (%s %s)", quantity, *unit)
			} else {
				qtyStr = fmt.Sprintf(" (%s)", quantity)
			}
		} else if unit != nil && *unit != "" {
			qtyStr = fmt.Sprintf(" (1 %s)", *unit)
		}

		priceStr := ""
		if price != nil {
			priceStr = fmt.Sprintf(" \u2014 $%.2f", *price)
			totalEstimate += *price
			hasEstimate = true
		}

		lines = append(lines, fmt.Sprintf("%s %s%s%s", checkbox, name, qtyStr, priceStr))
		itemCount++
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Build header. When a store filter is applied, prefix the list name with
	// the store name and include item count. The estimate suffix is preserved
	// in both modes.
	var header string
	if storeID != "" {
		header = fmt.Sprintf("%s \u2014 %s (%d items)", listName, storeName, itemCount)
	} else {
		header = listName
	}
	if hasEstimate {
		header = fmt.Sprintf("%s \u2014 Est. $%.2f", header, totalEstimate)
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return c.String(http.StatusOK, sb.String())
}

// exportRow holds a single flattened receipt-item row. The same row structure
// backs both the CSV writer (one row per line item) and the markdown writer
// (rows grouped by receipt_id).
type exportRow struct {
	ReceiptID       string
	ReceiptDate     time.Time
	StoreName       string
	ItemName        string
	MatchedProduct  string
	Quantity        string
	Unit            string
	UnitPrice       *decimal.Decimal
	TotalPrice      decimal.Decimal
	Category        string
	Subtotal        *decimal.Decimal
	Tax             *decimal.Decimal
	Total           *decimal.Decimal
	LineNumber      *int
}

// ExportReceipts streams a bulk export of the household's receipts.
//
// GET /api/v1/export/receipts
//
//	?from=YYYY-MM-DD   optional — filter receipts with receipt_date >= from
//	&to=YYYY-MM-DD     optional — filter receipts with receipt_date <= to
//	&store_id=<id>     optional — filter by store
//	&format=csv|markdown (required)
//
// Household-scoped via cookie auth. No admin gate, no rate-limit — reads are
// cheap and the scoped query prevents cross-tenant access.
func (h *ExportHandler) ExportReceipts(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	format := c.QueryParam("format")
	if format != "csv" && format != "markdown" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "format query param is required and must be 'csv' or 'markdown'",
		})
	}

	from := c.QueryParam("from")
	to := c.QueryParam("to")
	if from != "" {
		if _, err := time.Parse("2006-01-02", from); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "from must be YYYY-MM-DD"})
		}
	}
	if to != "" {
		if _, err := time.Parse("2006-01-02", to); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "to must be YYYY-MM-DD"})
		}
	}
	storeID := c.QueryParam("store_id")

	rows, err := h.queryExportRows(householdID, from, to, storeID)
	if err != nil {
		slog.Default().Error("export receipts query failed", "err", err, "household_id", householdID)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	stamp := time.Now().UTC().Format("20060102")

	if format == "csv" {
		filename := fmt.Sprintf("receipts-export-%s.csv", stamp)
		c.Response().Header().Set(echo.HeaderContentType, "text/csv; charset=utf-8")
		c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
		c.Response().WriteHeader(http.StatusOK)
		return writeReceiptsCSV(c.Response().Writer, rows)
	}

	filename := fmt.Sprintf("receipts-export-%s.zip", stamp)
	c.Response().Header().Set(echo.HeaderContentType, "application/zip")
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Response().WriteHeader(http.StatusOK)
	return writeReceiptsMarkdownZip(c.Response().Writer, rows)
}

// queryExportRows runs the single joined query that backs both writers.
// Sorted deterministically so downstream grouping logic is stable.
func (h *ExportHandler) queryExportRows(householdID, from, to, storeID string) ([]exportRow, error) {
	q := `SELECT r.id, r.receipt_date, COALESCE(s.name, ''),
	             li.raw_name, COALESCE(p.name, ''),
	             li.quantity, COALESCE(li.unit, ''),
	             li.unit_price, li.total_price,
	             COALESCE(p.category, ''),
	             r.subtotal, r.tax, r.total,
	             li.line_number
	      FROM receipts r
	      INNER JOIN line_items li ON li.receipt_id = r.id
	      LEFT JOIN products p ON li.product_id = p.id
	      LEFT JOIN stores s ON r.store_id = s.id
	      WHERE r.household_id = ?`
	args := []interface{}{householdID}

	if from != "" {
		q += " AND r.receipt_date >= ?"
		args = append(args, from)
	}
	if to != "" {
		q += " AND r.receipt_date <= ?"
		args = append(args, to)
	}
	if storeID != "" {
		q += " AND r.store_id = ?"
		args = append(args, storeID)
	}
	q += " ORDER BY r.receipt_date DESC, r.id, li.line_number, li.created_at"

	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query receipts for export: %w", err)
	}
	defer rows.Close()

	var out []exportRow
	for rows.Next() {
		var r exportRow
		var unitPrice, totalPrice, subtotal, tax, total *decimal.Decimal
		if err := rows.Scan(
			&r.ReceiptID, &r.ReceiptDate, &r.StoreName,
			&r.ItemName, &r.MatchedProduct,
			&r.Quantity, &r.Unit,
			&unitPrice, &totalPrice,
			&r.Category,
			&subtotal, &tax, &total,
			&r.LineNumber,
		); err != nil {
			return nil, fmt.Errorf("scan export row: %w", err)
		}
		if totalPrice == nil {
			z := decimal.Zero
			r.TotalPrice = z
		} else {
			r.TotalPrice = *totalPrice
		}
		r.UnitPrice = unitPrice
		r.Subtotal = subtotal
		r.Tax = tax
		r.Total = total
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export rows: %w", err)
	}
	return out, nil
}

// writeReceiptsCSV emits header + one row per line item. Empty result still
// produces a valid CSV with the header row.
func writeReceiptsCSV(w io.Writer, rows []exportRow) error {
	cw := csv.NewWriter(w)
	header := []string{
		"receipt_id", "receipt_date", "store_name", "item_name",
		"matched_product", "quantity", "unit", "unit_price",
		"total_price", "category",
	}
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}
	for _, r := range rows {
		rec := []string{
			r.ReceiptID,
			r.ReceiptDate.Format("2006-01-02"),
			r.StoreName,
			r.ItemName,
			r.MatchedProduct,
			r.Quantity,
			r.Unit,
			decimalOrEmpty(r.UnitPrice),
			r.TotalPrice.String(),
			r.Category,
		}
		if err := cw.Write(rec); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	return nil
}

// writeReceiptsMarkdownZip emits one markdown file per receipt into a zip
// stream. Empty result still produces a valid (empty) zip.
func writeReceiptsMarkdownZip(w io.Writer, rows []exportRow) error {
	zw := zip.NewWriter(w)

	// Group by receipt_id, preserving the ORDER BY sort from the query.
	type group struct {
		receiptID string
		header    exportRow
		items     []exportRow
	}
	var groups []group
	indexByID := make(map[string]int)
	for _, r := range rows {
		if idx, ok := indexByID[r.ReceiptID]; ok {
			groups[idx].items = append(groups[idx].items, r)
			continue
		}
		indexByID[r.ReceiptID] = len(groups)
		groups = append(groups, group{
			receiptID: r.ReceiptID,
			header:    r,
			items:     []exportRow{r},
		})
	}

	usedNames := make(map[string]struct{})
	for _, g := range groups {
		name := filenameForReceipt(g.header, usedNames)
		usedNames[name] = struct{}{}

		fw, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("zip create %s: %w", name, err)
		}
		if _, err := fw.Write([]byte(renderReceiptMarkdown(g.header, g.items))); err != nil {
			return fmt.Errorf("zip write %s: %w", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip close: %w", err)
	}
	return nil
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases, replaces any run of non-alphanumerics with a single
// hyphen, and trims. Empty or all-punctuation input returns "unknown-store".
func slugify(s string) string {
	lower := strings.ToLower(s)
	replaced := slugNonAlnum.ReplaceAllString(lower, "-")
	trimmed := strings.Trim(replaced, "-")
	if trimmed == "" {
		return "unknown-store"
	}
	return trimmed
}

// filenameForReceipt constructs a YYYY-MM-DD-<slug>.md name, appending the
// first 8 chars of a sha256 of the receipt_id if the primary name would
// collide with an existing entry in the same zip.
func filenameForReceipt(r exportRow, used map[string]struct{}) string {
	slug := "unknown-store"
	if r.StoreName != "" {
		slug = slugify(r.StoreName)
	}
	base := fmt.Sprintf("%s-%s", r.ReceiptDate.Format("2006-01-02"), slug)
	name := base + ".md"
	if _, clash := used[name]; clash {
		sum := sha256.Sum256([]byte(r.ReceiptID))
		suffix := hex.EncodeToString(sum[:])[:8]
		name = fmt.Sprintf("%s-%s.md", base, suffix)
	}
	return name
}

// renderReceiptMarkdown produces the per-receipt markdown body: YAML
// frontmatter followed by a header line and a pipe table.
func renderReceiptMarkdown(header exportRow, items []exportRow) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "date: %s\n", header.ReceiptDate.Format("2006-01-02"))
	if header.StoreName != "" {
		fmt.Fprintf(&sb, "store: %s\n", yamlScalar(header.StoreName))
	}
	if header.Total != nil {
		fmt.Fprintf(&sb, "total: %s\n", money(*header.Total))
	}
	if header.Subtotal != nil {
		fmt.Fprintf(&sb, "subtotal: %s\n", money(*header.Subtotal))
	}
	if header.Tax != nil {
		fmt.Fprintf(&sb, "tax: %s\n", money(*header.Tax))
	}
	fmt.Fprintf(&sb, "receipt_id: %s\n", header.ReceiptID)
	sb.WriteString("---\n\n")

	title := header.StoreName
	if title == "" {
		title = "Unknown Store"
	}
	fmt.Fprintf(&sb, "# %s \u2014 %s\n\n", title, header.ReceiptDate.Format("2006-01-02"))

	sb.WriteString("| Item | Matched | Qty | Unit | Unit Price | Total |\n")
	sb.WriteString("|------|---------|-----|------|------------|-------|\n")
	for _, it := range items {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
			mdCell(it.ItemName),
			mdCell(it.MatchedProduct),
			mdCell(it.Quantity),
			mdCell(it.Unit),
			mdCell(decimalOrEmpty(it.UnitPrice)),
			mdCell(money(it.TotalPrice)),
		)
	}
	return sb.String()
}

// mdCell escapes pipe and backslash for markdown table cells and collapses
// embedded newlines so a malformed raw_name can't break the table layout.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// yamlScalar quotes a frontmatter value when it contains characters that
// would otherwise require YAML parsing (colons, hashes, leading/trailing
// whitespace, or a literal `---`). Plain strings pass through unquoted.
func yamlScalar(s string) string {
	needsQuote := strings.ContainsAny(s, ":#\n\r\"'[]{}|>&*!%@`") ||
		strings.Contains(s, "---") ||
		s != strings.TrimSpace(s)
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// money formats monetary decimals as a fixed two-decimal string. Quantity
// values do NOT flow through this helper — they keep full precision.
func money(d decimal.Decimal) string {
	return d.StringFixed(2)
}

// decimalOrEmpty returns the canonical string form of a decimal, or empty
// when the pointer is nil. Used for nullable unit_price columns.
func decimalOrEmpty(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}

