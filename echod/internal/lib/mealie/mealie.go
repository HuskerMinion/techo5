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

// recipesBaseURL and recipeImageBaseURL are where Mealie keeps its recipes and their pictures, below
// its address.
const recipesBaseURL = "/api/recipes"
const recipeImageBaseURL = "/api/media/recipes"

// recipeImageSize is the picture of a recipe that Mealie makes for a small view: the middle square of
// the original, about 600 pixels across.
const recipeImageSize = "tiny-original.webp"

// Client is one Mealie server: its address and an API token. Mealie asks for the token to list recipes,
// so it is empty only for a server that needs none.
type Client struct {
	URL, Token string
}

// recipePage is one page of Mealie's list of recipes: how many pages there are, and the recipes on it.
type recipePage struct {
	TotalPages int           `json:"total_pages"`
	Items      []RecipeEntry `json:"items"`
}

// RecipeEntry is a recipe as Mealie's list has it: what the overview shows, and what to ask for its
// guide and its picture.
type RecipeEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// ImageVersion is Mealie's version of the recipe's picture, empty for a recipe with none.
	ImageVersion string `json:"image"`
}

// DetailedRecipe is one recipe as Mealie's detail has it: what the step-by-step guide shows.
type DetailedRecipe struct {
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

// get fetches the given endpoint and decodes the JSON answer into into. What goes wrong is told in words for a
// screen, without the address that was asked; what the server said goes to the log.
func (c Client) get(ctx context.Context, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return didNotAnswer(err)
	}
	req.Header.Set("User-Agent", "TECHO5 (github.com/HuskerMinion/techo5)")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
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
		slog.Debug("mealie: an answer that was not wanted", "status", resp.Status, "said", strings.TrimSpace(string(b[:min(len(b), 200)])))
		return fmt.Errorf("mealie answered %s", resp.Status)
	}
	if err := json.Unmarshal(b, into); err != nil {
		slog.Warn("mealie: an answer that could not be read", "err", err)
		return ErrUnreadable
	}
	return nil
}

// didNotAnswer returns err as a Mealie that did not answer, without the address that was asked.
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
	var recipes []RecipeEntry
	for page := 1; ; page++ {
		var out recipePage
		if err := c.get(ctx, c.pageURL(page), &out); err != nil {
			return nil, err
		}
		recipes = append(recipes, out.Items...)
		if page >= out.TotalPages || len(out.Items) == 0 {
			return recipes, nil
		}
	}
}

// pageURL returns the URL of the given page of the list of recipes, by name. The first page is 1.
func (c Client) pageURL(page int) string {
	return strings.TrimRight(c.URL, "/") + recipesBaseURL + "?page=" + strconv.Itoa(page) + "&perPage=" + strconv.Itoa(fetchedPerPage) + "&orderBy=name&orderDirection=asc"
}

// ImageURL returns the URL of the recipe's small picture, which Mealie makes square from the middle of
// the original, or an empty string for a recipe that has none. The version is only there so that a
// picture that was changed is not taken from a cache.
func (c Client) ImageURL(recipe RecipeEntry) string {
	if recipe.ImageVersion == "" {
		return ""
	}
	return strings.TrimRight(c.URL, "/") + recipeImageBaseURL + "/" + url.PathEscape(recipe.ID) + "/images/" + recipeImageSize + "?version=" + url.QueryEscape(recipe.ImageVersion)
}

// recipeURL returns the URL of the recipe with the given slug.
func (c Client) recipeURL(slug string) string {
	return strings.TrimRight(c.URL, "/") + recipesBaseURL + "/" + url.PathEscape(slug)
}

// Recipe returns the recipe with the given slug. Ingredients with no text and steps with neither text nor
// title are dropped; a dropped ingredient's section heading moves to the next one kept.
func (c Client) Recipe(ctx context.Context, slug string) (DetailedRecipe, error) {
	var recipe DetailedRecipe
	if err := c.get(ctx, c.recipeURL(slug), &recipe); err != nil {
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
