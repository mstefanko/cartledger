package models

import (
	"time"

	"github.com/shopspring/decimal"
)

// Household represents a shared household space.
type Household struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// User represents a user within a household.
type User struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store represents a grocery store.
type Store struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
	Icon         *string   `json:"icon,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Product represents a canonical product in the catalog.
type Product struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"household_id"`
	Name            string     `json:"name"`
	Category        *string    `json:"category,omitempty"`
	DefaultUnit     *string    `json:"default_unit,omitempty"`
	Notes           *string    `json:"notes,omitempty"`
	LastPurchasedAt *time.Time `json:"last_purchased_at,omitempty"`
	PurchaseCount   int        `json:"purchase_count"`
	Brand           *string    `json:"brand,omitempty" db:"brand"`
	ProductTags     *string    `json:"product_tags,omitempty" db:"product_tags"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ProductAlias maps a receipt abbreviation to a canonical product.
type ProductAlias struct {
	ID        string    `json:"id"`
	ProductID string    `json:"product_id"`
	Alias     string    `json:"alias"`
	StoreID   *string   `json:"store_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MatchingRule defines an auto-categorization rule.
type MatchingRule struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	Priority     int       `json:"priority"`
	ConditionOp  string    `json:"condition_op"`
	ConditionVal string    `json:"condition_val"`
	StoreID      *string   `json:"store_id,omitempty"`
	ProductID    string    `json:"product_id"`
	Category     *string   `json:"category,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Receipt represents a scanned receipt.
type Receipt struct {
	ID           string          `json:"id"`
	HouseholdID  string          `json:"household_id"`
	StoreID      *string         `json:"store_id,omitempty"`
	ScannedBy    *string         `json:"scanned_by,omitempty"`
	ReceiptDate  time.Time       `json:"receipt_date"`
	Subtotal     *decimal.Decimal `json:"subtotal,omitempty"`
	Tax          *decimal.Decimal `json:"tax,omitempty"`
	Total        *decimal.Decimal `json:"total,omitempty"`
	ImagePaths   *string         `json:"image_paths,omitempty"`
	RawLLMJSON   *string         `json:"raw_llm_json,omitempty"`
	Status       string          `json:"status"`
	LLMProvider  *string         `json:"llm_provider,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// LineItem represents an individual item on a receipt.
type LineItem struct {
	ID         string           `json:"id"`
	ReceiptID  string           `json:"receipt_id"`
	ProductID  *string          `json:"product_id,omitempty"`
	RawName    string           `json:"raw_name"`
	Quantity   decimal.Decimal  `json:"quantity"`
	Unit       *string          `json:"unit,omitempty"`
	UnitPrice  *decimal.Decimal `json:"unit_price,omitempty"`
	TotalPrice decimal.Decimal  `json:"total_price"`
	Matched    string           `json:"matched"`
	Confidence *float64         `json:"confidence,omitempty"`
	LineNumber         *int             `json:"line_number,omitempty"`
	SuggestedName      *string          `json:"suggested_name,omitempty" db:"suggested_name"`
	SuggestedCategory  *string          `json:"suggested_category,omitempty" db:"suggested_category"`
	SuggestedProductID *string          `json:"suggested_product_id,omitempty" db:"suggested_product_id"`
	CreatedAt          time.Time        `json:"created_at"`
}

// ProductPrice stores a denormalized price record for analytics.
type ProductPrice struct {
	ID              string           `json:"id"`
	ProductID       string           `json:"product_id"`
	StoreID         string           `json:"store_id"`
	ReceiptID       string           `json:"receipt_id"`
	ReceiptDate     time.Time        `json:"receipt_date"`
	Quantity        decimal.Decimal  `json:"quantity"`
	Unit            string           `json:"unit"`
	UnitPrice       decimal.Decimal  `json:"unit_price"`
	NormalizedPrice *decimal.Decimal `json:"normalized_price,omitempty"`
	NormalizedUnit  *string          `json:"normalized_unit,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}

// ShoppingList represents a shopping list.
type ShoppingList struct {
	ID          string    `json:"id"`
	HouseholdID string    `json:"household_id"`
	Name        string    `json:"name"`
	CreatedBy   *string   `json:"created_by,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ShoppingListItem represents an item on a shopping list.
type ShoppingListItem struct {
	ID        string          `json:"id"`
	ListID    string          `json:"list_id"`
	ProductID *string         `json:"product_id,omitempty"`
	Name      string          `json:"name"`
	Quantity  decimal.Decimal `json:"quantity"`
	Unit      *string         `json:"unit,omitempty"`
	Checked   bool            `json:"checked"`
	CheckedBy *string         `json:"checked_by,omitempty"`
	SortOrder int             `json:"sort_order"`
	Notes     *string         `json:"notes,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// UnitConversion stores food-specific density conversions.
type UnitConversion struct {
	ID        string          `json:"id"`
	ProductID *string         `json:"product_id,omitempty"`
	FromUnit  string          `json:"from_unit"`
	ToUnit    string          `json:"to_unit"`
	Factor    decimal.Decimal `json:"factor"`
}

// ProductImage represents a user-uploaded product photo.
type ProductImage struct {
	ID        string    `json:"id"`
	ProductID string    `json:"product_id"`
	ImagePath string    `json:"image_path"`
	Type      string    `json:"type"`
	Caption   *string   `json:"caption,omitempty"`
	IsPrimary bool      `json:"is_primary"`
	CreatedAt time.Time `json:"created_at"`
}

// ProductLink represents a back-reference to Mealie or other external sources.
type ProductLink struct {
	ID         string    `json:"id"`
	ProductID  string    `json:"product_id"`
	Source     string    `json:"source"`
	ExternalID *string   `json:"external_id,omitempty"`
	URL        string    `json:"url"`
	Label      *string   `json:"label,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
