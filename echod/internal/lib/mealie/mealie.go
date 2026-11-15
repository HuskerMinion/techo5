package mealie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// client is shared by every request to Mealie, so that its connections are reused.
var client = &http.Client{Timeout: 15 * time.Second}

// fetchedPerPage is how many recipes each request for the list asks for.
const fetchedPerPage = 100

// recipesBaseUrl and recipeImageBaseUrl are where Mealie keeps its recipes and their pictures, below
// its address.
const recipesBaseUrl = "/api/recipes"
const recipeImageBaseUrl = "/api/media/recipes"

// Client is one Mealie server: its address and an optional token.
type Client struct {
	URL, Token string
}

// RecipeFetchResult is one page of Mealie's list of recipes: which page it is, how many pages there are,
// and the recipes on it.
type RecipeFetchResult struct {
	Page       int           `json:"page"`
	TotalPages int           `json:"total_pages"`
	Items      []RecipeEntry `json:"items"`
}

// RecipeEntry is a recipe as Mealie's list has it.
type RecipeEntry struct {
	ID                 string  `json:"id"`
	UserID             string  `json:"userId"`
	HouseholdID        string  `json:"householdId"`
	GroupID            string  `json:"groupId"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	ImageVersion       string  `json:"image"`
	TotalTimeSeconds   int     `json:"totalTimeSeconds"`
	PrepTimeSeconds    int     `json:"prepTimeSeconds"`
	PerformTimeSeconds int     `json:"performTimeSeconds"`
	Description        string  `json:"description"`
	Rating             float32 `json:"rating"`
	// ISO time
	DateAdded string `json:"dateAdded"`
}

// get sends an HTTP request to the url and returns its answer as mapped to into. An answer other than 200 is an error that
// says the status and the start of what the server said.
func (c Client) get(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TECHO5 (github.com/HuskerMinion/techo5)")
	if c.Token != "" {
		req.Header.Add("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mealie: %s: %.200s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, into)
}

// Recipes returns all the recipes in name order, reading the list a page at a time. An error on any page
// is an error for the whole list.
func (c Client) Recipes(ctx context.Context) ([]RecipeEntry, error) {
	page := 1
	var recipes []RecipeEntry
	for {
		var out RecipeFetchResult

		err := c.get(ctx, c.RecipeFetchURL(page), &out)
		if err != nil {
			return nil, err
		}
		recipes = append(recipes, out.Items...)

		if page >= out.TotalPages {
			return recipes, nil
		}

		page = page + 1
	}
}

// RecipeFetchURL returns the URL of the given page of the list of recipes, by name. The first page
// is 1.
func (c Client) RecipeFetchURL(page int) string {
	return strings.TrimRight(c.URL, "/") + recipesBaseUrl + "?page=" + strconv.Itoa(page) + "&perPage=" + strconv.Itoa(fetchedPerPage) + "&orderBy=name&orderDirection=asc"
}

// ImageURL returns the URL of the recipe's small picture.
func (c Client) ImageURL(recipe RecipeEntry) string {
	return strings.TrimRight(c.URL, "/") + recipeImageBaseUrl + "/" + recipe.ID + "/images/min-original.webp?version=" + recipe.ImageVersion
}
