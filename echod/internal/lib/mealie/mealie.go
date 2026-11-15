package mealie

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

// Client is one Mealie server: its address and an API token, which Mealie asks for to list recipes. The
// token may be empty for some server configurations.
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

// DetailedRecipe is one recipe as Mealie's detail has it: what the step-by-step guide shows.
type DetailedRecipe struct {
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Ingredients []Ingredient `json:"recipeIngredient"`
	Steps       []Step       `json:"recipeInstructions"`
}

// Ingredient is one line of a recipe's ingredients.
type Ingredient struct {
	// Text is the line as Mealie writes it out ("2 cans tomatoes, drained"): amount, unit, food and note
	// together, so that it needs no more work to show.
	Text string `json:"display"`
	// Section is the heading this ingredient opens, such as "For the sauce", or empty when it opens none.
	Section string `json:"title"`
}

// Step is one step of a recipe's instructions.
type Step struct {
	// Title is a heading for the step, which may be empty.
	Title string `json:"title"`
	Text  string `json:"text"`
}

// get sends an HTTP request to the url and returns its answer as mapped to into. What goes wrong is told
// in words for a screen. The actual error goes to the log.
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
		return didNotAnswer(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return didNotAnswer(err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		if c.Token == "" {
			return errNeedsToken
		}
		return errRefusedToken
	case http.StatusForbidden:
		return errForbidden
	default:
		slog.Warn("mealie: an answer that was not wanted", "status", resp.Status, "said", strings.TrimSpace(string(b[:min(len(b), 200)])))
		return fmt.Errorf("mealie answered %s", resp.Status)
	}
	if err := json.Unmarshal(b, into); err != nil {
		slog.Warn("mealie: an answer that could not be read", "err", err)
		return ErrUnreadable
	}
	return nil
}

// didNotAnswer is err as a Mealie that did not answer
func didNotAnswer(err error) error {
	if asked, ok := errors.AsType[*url.Error](err); ok {
		err = asked.Err
	}
	return fmt.Errorf("mealie did not answer: %w", err)
}

// The ways Mealie refuses, told for a screen.
var (
	errNeedsToken   = errors.New("mealie asks for an API token: add one on the setup page")
	errRefusedToken = errors.New("mealie refused the token")
	errForbidden    = errors.New("mealie does not let this token read the recipes")
)

// ErrUnreadable is an answer from Mealie that this device could not read.
//
//lint:ignore ST1005 it starts with a name, and the screen shows it as it is
var ErrUnreadable = errors.New("Mealie sent something this device couldn't read")

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

// RecipeURL returns the URL of the recipe with the given slug.
func (c Client) RecipeURL(slug string) string {
	return strings.TrimRight(c.URL, "/") + recipesBaseUrl + "/" + url.PathEscape(slug)
}

// Recipe returns the recipe with the given slug, its ingredients and steps. An ingredient with no text
// and a step with neither text nor title are left out. The heading of a left out ingredient goes on to the next one that stays.
func (c Client) Recipe(ctx context.Context, slug string) (DetailedRecipe, error) {
	var recipe DetailedRecipe
	if err := c.get(ctx, c.RecipeURL(slug), &recipe); err != nil {
		return DetailedRecipe{}, err
	}
	var ingredients []Ingredient
	heading := ""
	for _, in := range recipe.Ingredients {
		heading = cmp.Or(strings.TrimSpace(in.Section), heading)
		text := strings.TrimSpace(in.Text)
		if text == "" {
			continue
		}
		ingredients = append(ingredients, Ingredient{Text: text, Section: heading})
		heading = ""
	}
	var steps []Step
	for _, st := range recipe.Steps {
		step := Step{Title: strings.TrimSpace(st.Title), Text: strings.TrimSpace(st.Text)}
		if step.Title != "" || step.Text != "" {
			steps = append(steps, step)
		}
	}
	recipe.Ingredients, recipe.Steps = ingredients, steps
	return recipe, nil
}
