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

// stumpedRecipe returns the JSON of a recipe as the list sends it, with the given id and name: the keys
// this package reads, and some it does not, one of them null.
func stumpedRecipe(id, name string) string {
	return `{"id":"` + id + `","name":"` + name + `","slug":"` + id + `","image":"v1","rating":null,"tags":[],"totalTimeSeconds":3000}`
}

// pagedResult returns the JSON Mealie sends for one page of the recipe list, holding the given items.
func pagedResult(page, totalPages int, items ...string) string {
	return `{"page":` + strconv.Itoa(page) + `,"per_page":2,"total":3,"total_pages":` + strconv.Itoa(totalPages) +
		`,"items":[` + strings.Join(items, ",") + `]}`
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
	if want := (RecipeEntry{ID: "a", Name: "Apple Pie", Slug: "a", ImageVersion: "v1"}); got[0] != want {
		t.Errorf("the first recipe is %+v, want %+v", got[0], want)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("the server was asked %d times, want 2", n)
	}
}

// A server that says there are more pages but sends none is not asked for them all.
func TestRecipesStopsOnAnEmptyPage(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		reply(w, pagedResult(1, 1000))
	})
	if _, err := c.Recipes(t.Context()); err != nil {
		t.Fatalf("Recipes: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the server was asked %d times, want once", n)
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

// A recipe is read from the detail at its slug, with the token when there is one, into the ingredients
// as written out and the steps, in order.
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
		Ingredients: []Ingredient{{Text: "2 cans tomato drained", Section: "For the soup"}, {Text: "¹/₂ onion"}},
		Steps:       []Step{{Title: "Prepare", Text: "Chop the onion."}, {Text: "Simmer for 20 minutes."}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	c.Token = ""
	if _, err := c.Recipe(t.Context(), "tomato-soup"); err != nil || auth != "" {
		t.Errorf("without a token: error %v, Authorization %q, want none", err, auth)
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

// A recipe's picture is the small square one at its id, with the version for caches; one without a
// picture has no address, and an address typed with a slash at the end is the same. What the server
// calls the recipe is one part of the path or the query, whatever it holds.
func TestImageURL(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		recipe     RecipeEntry
		want       string
	}{
		{"a picture", "http://mealie:9925", RecipeEntry{ID: "id-1", ImageVersion: "bTDV"},
			"http://mealie:9925/api/media/recipes/id-1/images/tiny-original.webp?version=bTDV"},
		{"a slash at the end", "http://mealie:9925/", RecipeEntry{ID: "id-1", ImageVersion: "bTDV"},
			"http://mealie:9925/api/media/recipes/id-1/images/tiny-original.webp?version=bTDV"},
		{"odd characters", "http://mealie:9925", RecipeEntry{ID: "a/b c", ImageVersion: "x&y=z"},
			"http://mealie:9925/api/media/recipes/a%2Fb%20c/images/tiny-original.webp?version=x%26y%3Dz"},
		{"no picture", "http://mealie:9925", RecipeEntry{ID: "id-1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Client{URL: tc.base}).ImageURL(tc.recipe); got != tc.want {
				t.Errorf("ImageURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// A recipe that cannot be read is an error and no recipe; the errors themselves are the list's.
func TestRecipeErrorIsNoRecipe(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Not Found"}`, http.StatusNotFound)
	})
	got, err := c.Recipe(t.Context(), "tomato-soup")
	if err == nil || !reflect.DeepEqual(got, DetailedRecipe{}) {
		t.Errorf("got %+v and error %v, want an error and no recipe", got, err)
	}
}
