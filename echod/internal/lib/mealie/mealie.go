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

var (
	client = &http.Client{Timeout: 15 * time.Second}
)

const fetchedPerPage = 15
const recipesBaseUrl = "/api/recipes"
const recipeImageBaseUrl = "/api/media/recipes"

type Client struct {
	URL, Token string
}

type RecipeFetchResult struct {
	Page       int           `json:"page"`
	TotalPages int           `json:"total_pages"`
	Items      []RecipeEntry `json:"items"`
}

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

func (c Client) RecipeFetchURL(page int) string {
	return strings.TrimRight(c.URL, "/") + recipesBaseUrl + "?page=" + strconv.Itoa(page) + "&perPage=" + strconv.Itoa(fetchedPerPage) + "&orderBy=name&orderDirection=asc"
}

func (c Client) ImageURL(recipe RecipeEntry) string {
	return strings.TrimRight(c.URL, "/") + recipeImageBaseUrl + "/" + recipe.ID + "/images/min-original.webp?version=" + recipe.ImageVersion
}
