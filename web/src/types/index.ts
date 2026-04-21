// TypeScript types mirroring Go models from internal/models/models.go
// - string for decimal fields (shopspring/decimal serializes to string in JSON)
// - string for date/time fields (ISO 8601 strings from Go's time.Time)
// - null for optional pointer fields (*T in Go → T | null)

// --- Domain Models ---

export interface Household {
  id: string
  name: string
  created_at: string
}

export interface User {
  id: string
  household_id: string
  email: string
  name: string
  is_admin: boolean
  // password_hash is never serialized (json:"-")
  created_at: string
}

export interface Store {
  id: string
  household_id: string
  name: string
  display_order: number
  icon: string | null
  address: string | null
  city: string | null
  state: string | null
  zip: string | null
  store_number: string | null
  nickname: string | null
  latitude: number | null
  longitude: number | null
  created_at: string
  updated_at: string
}

export interface Product {
  id: string
  household_id: string
  name: string
  category: string | null
  default_unit: string | null
  brand?: string
  pack_quantity?: number
  pack_unit?: string
  product_group_id?: string
  notes: string | null
  last_purchased_at: string | null
  purchase_count: number
  created_at: string
  updated_at: string
}

// Extended product returned by the list endpoint (includes computed fields)
export interface ProductListItem extends Product {
  alias_count: number
  last_price: string | null
  brand?: string
}

export interface ProductAlias {
  id: string
  product_id: string
  alias: string
  store_id: string | null
  created_at: string
}

export interface MatchingRule {
  id: string
  household_id: string
  priority: number
  condition_op: string
  condition_val: string
  store_id: string | null
  product_id: string
  category: string | null
  created_at: string
}

export interface Receipt {
  id: string
  household_id: string
  store_id: string | null
  store_name: string | null
  scanned_by: string | null
  receipt_date: string
  subtotal: string | null
  tax: string | null
  total: string | null
  image_paths: string | null
  raw_llm_json: string | null
  status: 'pending' | 'matched' | 'reviewed' | 'processing' | 'error'
  llm_provider: string | null
  card_type: string | null
  card_last4: string | null
  receipt_time: string | null
  created_at: string
  // Populated when status='error' — the worker's most recent failure reason.
  // Cleared on successful reprocess (see POST /receipts/:id/reprocess).
  error_message?: string | null
}

export interface LineItem {
  id: string
  receipt_id: string
  product_id: string | null
  product_name: string | null
  category: string | null
  raw_name: string
  quantity: string
  unit: string | null
  unit_price: string | null
  total_price: string
  matched: 'unmatched' | 'auto' | 'manual' | 'rule'
  confidence: number | null
  regular_price: string | null
  discount_amount: string | null
  line_number: number | null
  suggested_name: string | null
  suggested_category: string | null
  suggested_product_id: string | null
  suggested_product_name: string | null
  suggestion_type: 'existing_match' | 'new_product' | 'cross_store_match' | null
  created_at: string
}

export interface AcceptSuggestionsRequest {
  line_item_ids: string[]
  edits?: Record<string, { name?: string; category?: string }>
}

export interface AcceptSuggestionsResponse {
  created_count: number
  matched_count: number
  products_created: { id: string; name: string }[]
  products_matched: { id: string; name: string }[]
}

export interface ProductPrice {
  id: string
  product_id: string
  store_id: string
  receipt_id: string
  receipt_date: string
  quantity: string
  unit: string
  unit_price: string
  normalized_price: string | null
  normalized_unit: string | null
  regular_price: string | null
  discount_amount: string | null
  is_sale: boolean
  created_at: string
}

export interface ShoppingList {
  id: string
  household_id: string
  name: string
  created_by: string | null
  status: 'active' | 'completed' | 'archived'
  preferred_store_id: string | null
  created_at: string
  updated_at: string
}

export interface ShoppingListItem {
  id: string
  list_id: string
  product_id: string | null
  name: string
  quantity: string
  unit: string | null
  checked: boolean
  checked_by: string | null
  sort_order: number
  notes: string | null
  created_at: string
}

export interface UnitConversion {
  id: string
  product_id: string | null
  from_unit: string
  to_unit: string
  factor: string
}

export interface ProductImage {
  id: string
  product_id: string
  image_path: string
  type: 'photo' | 'nutrition' | 'packaging'
  caption: string | null
  is_primary: boolean
  created_at: string
}

export interface ProductLink {
  id: string
  product_id: string
  source: string
  external_id: string | null
  url: string
  label: string | null
  created_at: string
}

// --- Product Group Types ---

export interface GroupMember {
  id: string
  name: string
  brand?: string
  store_name?: string
  pack_quantity?: number
  pack_unit?: string
  latest_price?: string
  price_per_unit?: string
  receipt_date?: string
}

export interface ProductGroup {
  id: string
  name: string
  comparison_unit?: string
  member_count: number
  members?: GroupMember[]
  units_mixed?: boolean
  created_at: string
  updated_at: string
}

export interface CreateGroupRequest {
  name: string
  comparison_unit?: string
}

export interface UpdateGroupRequest {
  name?: string
  comparison_unit?: string
}

export interface GroupSuggestion {
  group_id: string
  group_name: string
  member_count: number
  reason: string
}

// --- Product Detail Types ---

export interface PriceHistoryEntry {
  date: string
  store_name: string
  store_id: string
  quantity: string
  unit: string
  unit_price: string
  total_price: string
  regular_price: string | null
  discount_amount: string | null
  is_sale: boolean
}

export interface StorePriceComparison {
  store_id: string
  store_name: string
  latest_price: string
  latest_date: string
  is_cheapest: boolean
}

export interface ProductDetail {
  product: Product
  aliases: ProductAlias[]
  images: ProductImage[]
  links: ProductLink[]
  price_history: PriceHistoryEntry[]
  store_comparison: StorePriceComparison[]
  price_per_unit?: number
  stats: {
    total_purchases: number
    avg_price: string
    min_price: string
    max_price: string
    total_saved?: string
  }
}

// --- API Request Types ---

export interface LoginRequest {
  email: string
  password: string
}

export interface LoginResponse {
  token: string
  user: User
}

export interface SetupRequest {
  household_name: string
  user_name: string
  email: string
  password: string
}

export interface SetupResponse {
  token: string
  user: User
  household: Household
}

export interface StatusResponse {
  needs_setup: boolean
}

export interface InviteResponse {
  link: string
  expires_in: string
}

export interface ValidateInviteResponse {
  household_name: string
  invited_by: string
}

export interface JoinRequest {
  token: string
  user_name: string
  email: string
  password: string
}

export interface JoinResponse {
  token: string
  user: User
}

// --- Generic API Types ---

export interface ApiError {
  error: string
  details?: string
}

export interface PaginatedResponse<T> {
  data: T[]
  total: number
  page: number
  per_page: number
}

// --- Store Request Types ---

export interface CreateStoreRequest {
  name: string
  icon?: string
}

export interface UpdateStoreRequest {
  name?: string
  icon?: string
  nickname?: string
  address?: string
  city?: string
  state?: string
  zip?: string
}

export interface ReorderStoresRequest {
  ids: string[]
}

// --- Product Request Types ---

export interface CreateProductRequest {
  name: string
  category?: string
  default_unit?: string
  brand?: string
  pack_quantity?: number
  pack_unit?: string
  notes?: string
}

export interface UpdateProductRequest {
  name?: string
  category?: string
  default_unit?: string
  brand?: string
  pack_quantity?: number
  pack_unit?: string
  product_group_id?: string | null
  notes?: string
}

export interface CreateAliasRequest {
  alias: string
  store_id?: string
}

// --- Receipt Request Types ---

export interface UpdateLineItemRequest {
  product_id?: string
  quantity?: string
  unit?: string
  unit_price?: string
  total_price?: string
}

// --- Matching Types ---

export interface MatchLineItemRequest {
  product_id: string
}

export interface MatchLineItemResponse {
  line_item: LineItem
  create_rule?: boolean
}

export interface CreateRuleRequest {
  priority?: number
  condition_op: string
  condition_val: string
  store_id?: string
  product_id: string
  category?: string
}

export interface UpdateRuleRequest {
  priority?: number
  condition_op?: string
  condition_val?: string
  store_id?: string
  product_id?: string
  category?: string
}

// --- Shopping List Extended Types ---

export interface ShoppingListWithCounts extends ShoppingList {
  item_count: number
  checked_count: number
}

export interface ListItemWithPrice {
  id: string
  list_id: string
  product_id: string | null
  product_name: string | null
  name: string
  quantity: string
  unit: string | null
  checked: boolean
  checked_by: string | null
  sort_order: number
  notes: string | null
  estimated_price: string | null
  cheapest_store: string | null
  cheapest_price: string | null
  store_price: string | null
  store_price_store: string | null
  created_at: string
  product_group_id: string | null
  product_group_name: string | null
  cheapest_product_id: string | null
  assigned_store_id: string | null
  assigned_store_name: string | null
  assigned_store_price: string | null
  store_history_count: number
  storage_zone: 'produce' | 'cold' | 'frozen' | 'other'
}

export interface ShoppingListDetail {
  id: string
  household_id: string
  name: string
  created_by: string | null
  status: 'active' | 'completed' | 'archived'
  preferred_store_id: string | null
  preferred_store_name: string | null
  items: ListItemWithPrice[]
  created_at: string
  updated_at: string
}

export interface CreateListRequest {
  name: string
}

export interface UpdateListRequest {
  name?: string
  status?: 'active' | 'completed' | 'archived'
  preferred_store_id?: string | null
}

export interface CreateListItemRequest {
  name: string
  product_id?: string
  product_group_id?: string
  quantity?: string
  unit?: string
  notes?: string
  assigned_store_id?: string | null
}

export interface UpdateListItemRequest {
  name?: string
  product_id?: string
  product_group_id?: string | null
  quantity?: string
  unit?: string
  checked?: boolean
  checked_by?: string
  notes?: string
  sort_order?: number
  assigned_store_id?: string | null
}

export interface ReorderListItemsRequest {
  items: { id: string; sort_order: number }[]
}

// --- Mealie Integration Types ---

export interface MealieStatus {
  configured: boolean
  connected: boolean
  mealie_url: string | null
  error: string | null
}

export interface MealieRecipe {
  id: string
  slug: string
  name: string
  description: string | null
  image: string | null
  total_time: string | null
  servings: number | null
}

export interface MealieFood {
  id: string
  name: string
  description: string | null
}

export interface MealieIngredient {
  quantity: string | null
  unit: string | null
  food: MealieFood | null
  note: string | null
  original_text: string | null
}

export interface MealieRecipeDetail extends MealieRecipe {
  ingredients: MealieIngredient[]
}

export interface MealieShoppingList {
  id: string
  name: string
  item_count: number
}

export interface ImportedItem {
  product_id: string | null
  product_name: string | null
  mealie_food_name: string
  quantity: string | null
  unit: string | null
  cost: string | null
  matched: boolean
}

export interface ImportedRecipe {
  recipe_name: string
  recipe_slug: string
  total_cost: string | null
  items: ImportedItem[]
}

export interface ImportedShoppingList {
  list_id: string
  list_name: string
  items_imported: number
  items_matched: number
}

// --- Unit Conversion Types ---

export interface CreateConversionRequest {
  product_id?: string
  from_unit: string
  to_unit: string
  factor: string
}

// --- WebSocket Message Types ---

export interface WSMessage {
  type: string
  payload: unknown
}

export interface WSReceiptProcessed {
  type: 'receipt.processed'
  payload: {
    receipt_id: string
  }
}

export interface WSListUpdated {
  type: 'list.updated'
  payload: {
    list_id: string
  }
}

export interface WSListItemUpdated {
  type: 'list.item.updated'
  payload: {
    list_id: string
    item_id: string
  }
}

export interface WSListItemsBulkUpdated {
  type: 'list.items.bulk_updated'
  payload: { list_id: string; item_ids: string[] }
}

export interface WSListItemsBulkRemoved {
  type: 'list.items.bulk_removed'
  payload: { list_id: string; item_ids: string[] }
}

export interface WSListLockAcquired {
  type: 'list.lock.acquired'
  payload: { list_id: string; user_id: string; user_name: string }
}

export interface WSListLockReleased {
  type: 'list.lock.released'
  payload: { list_id: string; user_id: string }
}

export interface WSListLockTakenOver {
  type: 'list.lock.taken_over'
  payload: { list_id: string; new_user_id: string; new_user_name: string; prior_user_id: string }
}

// --- Analytics Types ---

export interface AnalyticsOverview {
  spent_this_month: number
  spent_last_month: number
  percent_change: number
  trip_count: number
  avg_trip_cost: number
  unique_products_purchased: number
}

export interface SparklinePoint {
  date: string
  price: string
  store: string
  is_sale: boolean
}

export interface ProductTrend {
  product_id: string
  product_name: string
  sparkline: SparklinePoint[]
  percent_change: number
  min_price: string
  max_price: string
  avg_price: string
  current_price: string
}

// Backend returns flat product+trend shape from /analytics/products
export interface ProductWithTrend {
  id: string
  name: string
  category?: string
  latest_price: number
  avg_price: number
  percent_change: number
  last_purchased?: string
  sparkline?: SparklinePoint[]
  min_price?: number
  max_price?: number
}

export interface StoreSummary {
  store: Store
  total_spent: number
  trip_count: number
  avg_trip_cost: number
  price_leaders: { product_id: string; product_name: string; avg_price: number }[]
  recent_trips: { receipt_id: string; date: string; total: number; item_count: number }[]
}

export interface Trip {
  receipt_id: string
  store_id: string | null
  store_name: string
  date: string
  total: number
  item_count: number
}

export interface Deal {
  product_id: string
  product_name: string
  store_name: string
  current_price: string
  avg_price: string
  savings_percent: number
  is_sale: boolean
}

export interface BuyAgainItem {
  product_id: string
  product_name: string
  avg_days_between: number
  days_since_last: number
  est_supply_days: number
  urgency_ratio: number
  last_quantity: string
  last_store: string
  // Added in Phase D — backend now also returns these fields.
  urgency?: string
  avg_days_per_unit?: number
  last_price?: string | null
  last_store_name?: string | null
}

export interface PricePoint {
  date: string
  normalized_price: number
  store: string
  is_sale: boolean
}

export interface RhythmTrips {
  current: number
  prior: number
  delta_pct: number | null
}

export interface RhythmBasket {
  current: number
  prior: number
}

export interface Rhythm {
  trips: RhythmTrips
  avg_basket: RhythmBasket
  avg_items_per_trip: number
  most_shopped_dow: string | null
  history_days: number
}

export interface ProductTrendResponse {
  price_history: PricePoint[]
  percent_change: number
  min_price: number
  min_store: string
  max_price: number
  max_store: string
  avg_price: number
}

// --- Category Breakdown Types ---

export interface CategoryBucket {
  name: string
  current: number
  prior: number
  pct_of_total: number
  delta_pct: number | null
}

export interface CategoryBreakdown {
  window_days: number
  total: number
  categories: CategoryBucket[]
}

export interface Savings {
  month_to_date: number
  last_30d: number
  year_to_date: number
}

// --- Price Moves Types ---

export interface PriceMove {
  product_id: string
  name: string
  avg_0_30d: number
  avg_30_90d: number
  pct_change: number
  unit: string
  observations_recent: number
  observations_prior: number
}

export interface PriceMoves {
  up: PriceMove[]
  down: PriceMove[]
}

// --- Staples Types ---

export interface Staple {
  product_id: string
  name: string
  category: string
  times_bought: number
  cadence_days: number
  total_spent: number
  avg_price: number
  weekly_spend: number | null
  monthly_spend: number | null
  yearly_projection: number | null
  sparkline_points: number[]
}
