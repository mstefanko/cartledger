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
  // password_hash is never serialized (json:"-")
  created_at: string
}

export interface Store {
  id: string
  household_id: string
  name: string
  display_order: number
  icon: string | null
  created_at: string
  updated_at: string
}

export interface Product {
  id: string
  household_id: string
  name: string
  category: string | null
  default_unit: string | null
  notes: string | null
  last_purchased_at: string | null
  purchase_count: number
  created_at: string
  updated_at: string
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
  scanned_by: string | null
  receipt_date: string
  subtotal: string | null
  tax: string | null
  total: string | null
  image_paths: string | null
  raw_llm_json: string | null
  status: 'pending' | 'matched' | 'reviewed'
  llm_provider: string | null
  created_at: string
}

export interface LineItem {
  id: string
  receipt_id: string
  product_id: string | null
  raw_name: string
  quantity: string
  unit: string | null
  unit_price: string | null
  total_price: string
  matched: 'unmatched' | 'auto' | 'manual' | 'rule'
  confidence: number | null
  line_number: number | null
  created_at: string
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
  created_at: string
}

export interface ShoppingList {
  id: string
  household_id: string
  name: string
  created_by: string | null
  status: 'active' | 'completed' | 'archived'
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

// --- Product Detail Types ---

export interface PriceHistoryEntry {
  date: string
  store_name: string
  store_id: string
  quantity: string
  unit: string
  unit_price: string
  total_price: string
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
  store_prices: StorePriceComparison[]
  stats: {
    count: number
    avg: string
    min: string
    max: string
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
}

export interface ReorderStoresRequest {
  ids: string[]
}

// --- Product Request Types ---

export interface CreateProductRequest {
  name: string
  category?: string
  default_unit?: string
  notes?: string
}

export interface UpdateProductRequest {
  name?: string
  category?: string
  default_unit?: string
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
  created_at: string
}

export interface ShoppingListDetail {
  id: string
  household_id: string
  name: string
  created_by: string | null
  status: 'active' | 'completed' | 'archived'
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
}

export interface CreateListItemRequest {
  name: string
  product_id?: string
  quantity?: string
  unit?: string
  notes?: string
}

export interface UpdateListItemRequest {
  name?: string
  product_id?: string
  quantity?: string
  unit?: string
  checked?: boolean
  checked_by?: string
  notes?: string
  sort_order?: number
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
