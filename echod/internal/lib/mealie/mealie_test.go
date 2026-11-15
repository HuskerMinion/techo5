package mealie

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// serve starts a fake Mealie answering with h and returns a client pointed at it.
func serve(t *testing.T, h http.HandlerFunc) Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return Client{URL: srv.URL}
}

// reply writes body as a JSON answer.
func reply(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// someRecipe is a recipe as the list sends it: the keys this package reads, others it does not (tags,
// recipeServings), and nulls where Mealie has nothing.
const someRecipe = `{
	"id": "id-lasagne",
	"userId": "user-1",
	"householdId": "household-1",
	"groupId": "group-1",
	"name": "Vegetable Lasagne",
	"slug": "vegetable-lasagne",
	"image": "bBVh",
	"recipeServings": 4,
	"recipeYieldQuantity": 0,
	"recipeYield": "",
	"totalTime": null,
	"prepTime": null,
	"cookTime": null,
	"performTime": null,
	"totalTimeSeconds": 3000,
	"prepTimeSeconds": 1200,
	"performTimeSeconds": 1800,
	"description": "A vegetarian lasagne.",
	"recipeCategory": [],
	"tags": [],
	"tools": [],
	"rating": 4.5,
	"orgURL": "https://example.com/lasagne/",
	"dateAdded": "2026-11-13"
}`

// stumpedRecipe returns the JSON of a minimal recipe with the given id and name.
func stumpedRecipe(id, name string) string {
	return `{"id":"` + id + `","name":"` + name + `","slug":"` + id + `","image":"v1"}`
}

// pagedResult returns the JSON Mealie sends for one page of the recipe list, holding the given items.
func pagedResult(page, totalPages int, items ...string) string {
	return `{"page":` + strconv.Itoa(page) + `,"per_page":2,"total":3,"total_pages":` + strconv.Itoa(totalPages) +
		`,"items":[` + strings.Join(items, ",") + `]}`
}

// No recipes at all is an empty list
func TestRecipesNone(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, `{"page":1,"per_page":2,"total":0,"total_pages":0,"items":[]}`)
	})
	got, err := c.Recipes(t.Context())
	if err != nil || len(got) != 0 {
		t.Errorf("got %d recipes, err %v; want none and no error", len(got), err)
	}
}

// A page of recipes is read into the fields the overview and the guide need.
func TestRecipesOnePage(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, pagedResult(1, 1, someRecipe))
	})
	got, err := c.Recipes(t.Context())
	if err != nil {
		t.Fatalf("Recipes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d recipes, want 1", len(got))
	}
	r := got[0]
	if r.ID != "id-lasagne" || r.Name != "Vegetable Lasagne" || r.Slug != "vegetable-lasagne" {
		t.Errorf("identity: %+v", r)
	}
	if r.ImageVersion != "bBVh" {
		t.Errorf("ImageVersion = %q, want the image key, bBVh", r.ImageVersion)
	}
	if r.TotalTimeSeconds != 3000 || r.PrepTimeSeconds != 1200 || r.PerformTimeSeconds != 1800 {
		t.Errorf("times: total %d, prep %d, perform %d", r.TotalTimeSeconds, r.PrepTimeSeconds, r.PerformTimeSeconds)
	}
	if r.Description != "A vegetarian lasagne." {
		t.Errorf("Description = %q", r.Description)
	}
	// A rating can be a half; a field that only fails on one must not fail the whole list.
	if r.Rating != 4.5 {
		t.Errorf("Rating = %v, want 4.5", r.Rating)
	}
}

// Every page is read, in order, and the loop stops after the last.
func TestRecipesFollowsPages(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) > 5 {
			t.Errorf("asked %d times: the loop does not stop", hits.Load())
			http.Error(w, "too many", http.StatusInternalServerError)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			reply(w, pagedResult(1, 2, stumpedRecipe("a", "Apple Pie"), stumpedRecipe("b", "Bean Stew")))
		case "2":
			reply(w, pagedResult(2, 2, stumpedRecipe("c", "Carrot Cake")))
		default:
			http.Error(w, "no such page", http.StatusNotFound)
		}
	})
	got, err := c.Recipes(t.Context())
	if err != nil {
		t.Fatalf("Recipes: %v", err)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	if want := "Apple Pie,Bean Stew,Carrot Cake"; strings.Join(names, ",") != want {
		t.Errorf("recipes = %v, want %s", names, want)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("the server was asked %d times, want 2", n)
	}
}

// An address typed with a slash at the end still reaches /api/recipes.
func TestRecipesTrailingSlash(t *testing.T) {
	var path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		reply(w, pagedResult(1, 1))
	})
	c.URL += "/"
	if _, err := c.Recipes(t.Context()); err != nil {
		t.Fatalf("Recipes: %v", err)
	}
	if path != "/api/recipes" {
		t.Errorf("path = %q, want /api/recipes", path)
	}
}

// The token is sent as a bearer token when there is one, and not at all when there is none.
func TestRecipesToken(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		want        []string
	}{
		{"with a token", "secret", []string{"Bearer secret"}},
		{"without one", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			c := serve(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Values("Authorization")
				reply(w, pagedResult(1, 1))
			})
			c.Token = tc.token
			if _, err := c.Recipes(t.Context()); err != nil {
				t.Fatalf("Recipes: %v", err)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("Authorization = %v, want %v", got, tc.want)
			}
		})
	}
}

// A server that refuses or fails is an error in words for a screen: what went wrong, by its status, and
// never what the server said about it.
func TestRecipesErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
		want   error
		says   string // for an error that is not one of the package's own: what it says
	}{
		{"refused token", "secret", http.StatusUnauthorized, `{"detail":"Unauthorized"}`, errRefusedToken, ""},
		{"a token is needed", "", http.StatusUnauthorized, `{"detail":"Unauthorized"}`, errNeedsToken, ""},
		{"forbidden", "secret", http.StatusForbidden, "keep out", errForbidden, ""},
		{"server failure", "secret", http.StatusInternalServerError, "boom", nil, "mealie answered 500 Internal Server Error"},
		{"not Mealie", "", http.StatusNotFound, "<html>not found</html>", nil, "mealie answered 404 Not Found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.body, tc.status)
			})
			c.Token = tc.token
			got, err := c.Recipes(t.Context())
			if err == nil {
				t.Fatalf("no error, and %d recipes", len(got))
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error %q, want %q", err, tc.want)
			}
			if tc.says != "" && err.Error() != tc.says {
				t.Errorf("error %q, want %q", err, tc.says)
			}
			if strings.Contains(err.Error(), tc.body) {
				t.Errorf("error %q has what the server said, %q", err, tc.body)
			}
		})
	}
}

// A Mealie that cannot be reached is an error that says so.
func TestRecipesNobodyAnswers(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c := Client{URL: srv.URL}
	srv.Close() // the address now answers nobody
	_, err := c.Recipes(t.Context())
	if err == nil || !strings.HasPrefix(err.Error(), "mealie did not answer: ") {
		t.Fatalf("error %v, want one that says Mealie did not answer", err)
	}
	if strings.Contains(err.Error(), "/api/recipes") || strings.Contains(err.Error(), "http://") {
		t.Errorf("error %q has the address that was asked", err)
	}
}

// An answer that is not the list is an error that says it could not be read.
func TestRecipesUnreadable(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, "this is not json")
	})
	got, err := c.Recipes(t.Context())
	if !errors.Is(err, ErrUnreadable) {
		t.Errorf("error %v and %d recipes, want ErrUnreadable", err, len(got))
	}
}

// someRecipeDetail is a recipe as a server sent one: the keys this package reads,
// others it does not (times, notes, nutrition), nulls where Mealie has nothing, an ingredient that opens
// a section, an amount written out with its unit and food, and the structured fields the text is made of.
const someRecipeDetail = `{
	"id": "id-soup",
	"slug": "tomato-soup",
	"name": "Tomato Soup",
	"image": null,
	"totalTimeSeconds": null,
	"notes": [],
	"nutrition": {"calories": null},
	"recipeIngredient": [
		{"quantity": 2, "unit": {"name": "can"}, "food": {"name": "tomato"}, "note": "drained",
		 "display": "2 cans tomato drained", "title": "For the soup", "referenceId": "r1"},
		{"quantity": 0.5, "unit": null, "food": null, "note": "onion",
		 "display": "¹/₂ onion", "title": null, "referenceId": "r2"}
	],
	"recipeInstructions": [
		{"id": "s1", "title": "Prepare", "summary": null, "text": "Chop the onion.", "ingredientReferences": []},
		{"id": "s2", "title": null, "summary": "", "text": "Simmer for 20 minutes.", "ingredientReferences": []}
	]
}`

// A recipe is read from the detail at its slug, with the token, into the name, the ingredients as
// written out and the steps, in order.
func TestRecipeReadsIngredientsAndSteps(t *testing.T) {
	var path, auth string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		reply(w, someRecipeDetail)
	})
	c.Token = "secret"
	got, err := c.Recipe(t.Context(), "tomato-soup")
	if err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	if path != "/api/recipes/tomato-soup" || auth != "Bearer secret" {
		t.Errorf("asked %q with %q, want /api/recipes/tomato-soup with the token", path, auth)
	}
	want := DetailedRecipe{
		Slug: "tomato-soup", Name: "Tomato Soup",
		Ingredients: []Ingredient{{Text: "2 cans tomato drained", Section: "For the soup"}, {Text: "¹/₂ onion"}},
		Steps:       []Step{{Title: "Prepare", Text: "Chop the onion."}, {Text: "Simmer for 20 minutes."}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Lines with nothing to show are left out, and the heading of one that goes is kept for the next. A
// recipe with no ingredients or steps has none, rather than blank ones.
func TestRecipeLeavesOutWhatIsBlank(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, `{"slug":"x","name":"X","recipeIngredient":[
			{"display":"","title":"Sauce"},
			{"display":"  "},
			{"display":" 1 pinch of salt ","title":null},
			{"display":"2 eggs"}],
		"recipeInstructions":[
			{"title":null,"text":"   "},
			{"title":"Bake","text":""},
			{"title":"","text":" Serve. "}]}`)
	})
	got, err := c.Recipe(t.Context(), "x")
	if err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	if want := []Ingredient{{Text: "1 pinch of salt", Section: "Sauce"}, {Text: "2 eggs"}}; !reflect.DeepEqual(got.Ingredients, want) {
		t.Errorf("ingredients %+v, want %+v", got.Ingredients, want)
	}
	if want := []Step{{Title: "Bake"}, {Text: "Serve."}}; !reflect.DeepEqual(got.Steps, want) {
		t.Errorf("steps %+v, want %+v", got.Steps, want)
	}

	c = serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, `{"slug":"y","name":"Y","recipeIngredient":[],"recipeInstructions":null}`)
	})
	got, err = c.Recipe(t.Context(), "y")
	if err != nil || len(got.Ingredients) != 0 || len(got.Steps) != 0 {
		t.Errorf("an empty recipe came out as %+v (%v)", got, err)
	}
}

// A slug is one part of the path, whatever it holds, and an address typed with a slash at the end
// still reaches /api/recipes.
func TestRecipeSlugIsOnePartOfThePath(t *testing.T) {
	var path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		reply(w, someRecipeDetail)
	})
	c.URL += "/"
	if _, err := c.Recipe(t.Context(), "a b/c?d"); err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	if path != "/api/recipes/a%20b%2Fc%3Fd" {
		t.Errorf("path = %q, want /api/recipes/a%%20b%%2Fc%%3Fd", path)
	}
}

// A recipe that is gone, a refused token and a reply that is not a recipe are errors in words for a
// screen, as for the list, and come with no recipe.
func TestRecipeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
		says   string
	}{
		{"gone", http.StatusNotFound, `{"detail":"Not Found"}`, nil, "mealie answered 404 Not Found"},
		{"refused token", http.StatusUnauthorized, `{"detail":"Unauthorized"}`, errRefusedToken, ""},
		{"not a recipe", http.StatusOK, "this is not json", ErrUnreadable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			c.Token = "secret"
			got, err := c.Recipe(t.Context(), "tomato-soup")
			if err == nil {
				t.Fatalf("no error, and recipe %+v", got)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error %q, want %q", err, tc.want)
			}
			if tc.says != "" && err.Error() != tc.says {
				t.Errorf("error %q, want %q", err, tc.says)
			}
			if !reflect.DeepEqual(got, DetailedRecipe{}) {
				t.Errorf("got %+v with the error, want none", got)
			}
		})
	}
}
