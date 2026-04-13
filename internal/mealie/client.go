package mealie

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MealieRecipe represents a recipe summary from the Mealie API.
type MealieRecipe struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description string  `json:"description"`
	Image       *string `json:"image,omitempty"`
}

// MealieRecipeDetail represents full recipe detail including ingredients.
type MealieRecipeDetail struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Slug         string             `json:"slug"`
	Description  string             `json:"description"`
	Image        *string            `json:"image,omitempty"`
	RecipeYield  string             `json:"recipeYield"`
	TotalTime    string             `json:"totalTime"`
	Ingredients  []MealieIngredient `json:"recipeIngredient"`
	Instructions []MealieStep       `json:"recipeInstructions"`
}

// MealieIngredient represents a recipe ingredient.
type MealieIngredient struct {
	Quantity float64     `json:"quantity"`
	Unit     *MealieUnit `json:"unit"`
	Food     *MealieFood `json:"food"`
	Note     string      `json:"note"`
	Display  string      `json:"display"`
}

// MealieUnit represents a unit from Mealie.
type MealieUnit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MealieFood represents a food item from Mealie.
type MealieFood struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"label,omitempty"`
}

// MealieStep represents a recipe instruction step.
type MealieStep struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// MealieShoppingList represents a Mealie shopping list.
type MealieShoppingList struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Items []MealieShoppingItem   `json:"listItems"`
}

// MealieShoppingItem represents an item on a Mealie shopping list.
type MealieShoppingItem struct {
	ID       string      `json:"id"`
	Quantity float64     `json:"quantity"`
	Unit     *MealieUnit `json:"unit"`
	Food     *MealieFood `json:"food"`
	Note     string      `json:"note"`
	Display  string      `json:"display"`
	Checked  bool        `json:"checked"`
}

// recipesResponse wraps the paginated response from Mealie's recipe list API.
type recipesResponse struct {
	Items []MealieRecipe `json:"items"`
	Total int            `json:"total"`
}

// foodsResponse wraps the paginated response from Mealie's foods API.
type foodsResponse struct {
	Items []MealieFood `json:"items"`
	Total int          `json:"total"`
}

// listsResponse wraps the paginated response from Mealie's shopping lists API.
type listsResponse struct {
	Items []MealieShoppingList `json:"items"`
	Total int                  `json:"total"`
}

// Client communicates with a Mealie instance via its REST API.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a Mealie API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Ping checks connectivity to the Mealie server.
func (c *Client) Ping() error {
	_, err := c.doGet("/api/app/about")
	return err
}

// ListRecipes returns all recipes from the Mealie instance.
func (c *Client) ListRecipes() ([]MealieRecipe, error) {
	body, err := c.doGet("/api/recipes?perPage=-1&orderBy=name&orderDirection=asc")
	if err != nil {
		return nil, fmt.Errorf("list recipes: %w", err)
	}
	var resp recipesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode recipes: %w", err)
	}
	return resp.Items, nil
}

// GetRecipe returns full recipe detail by slug.
func (c *Client) GetRecipe(slug string) (*MealieRecipeDetail, error) {
	body, err := c.doGet("/api/recipes/" + slug)
	if err != nil {
		return nil, fmt.Errorf("get recipe %s: %w", slug, err)
	}
	var recipe MealieRecipeDetail
	if err := json.Unmarshal(body, &recipe); err != nil {
		return nil, fmt.Errorf("decode recipe %s: %w", slug, err)
	}
	return &recipe, nil
}

// ListFoods returns all foods from the Mealie instance.
func (c *Client) ListFoods() ([]MealieFood, error) {
	body, err := c.doGet("/api/foods?perPage=-1")
	if err != nil {
		return nil, fmt.Errorf("list foods: %w", err)
	}
	var resp foodsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode foods: %w", err)
	}
	return resp.Items, nil
}

// ListShoppingLists returns all shopping lists from the Mealie instance.
func (c *Client) ListShoppingLists() ([]MealieShoppingList, error) {
	body, err := c.doGet("/api/households/shopping/lists?perPage=-1")
	if err != nil {
		return nil, fmt.Errorf("list shopping lists: %w", err)
	}
	var resp listsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode shopping lists: %w", err)
	}
	return resp.Items, nil
}

// GetShoppingList returns a single shopping list with all items.
func (c *Client) GetShoppingList(id string) (*MealieShoppingList, error) {
	body, err := c.doGet("/api/households/shopping/lists/" + id)
	if err != nil {
		return nil, fmt.Errorf("get shopping list %s: %w", id, err)
	}
	var list MealieShoppingList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode shopping list %s: %w", id, err)
	}
	return &list, nil
}

// doGet performs an authenticated GET request and returns the response body.
func (c *Client) doGet(path string) ([]byte, error) {
	url := c.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mealie API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
