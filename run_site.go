package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"potential-poetry/db"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"io"
	"bytes"
	"github.com/gorilla/mux"
)

// SubmitPageData holds data for the recipe submission page template.
type SubmitPageData struct {
	Success bool
	Error   string
	Recipe  *db.Recipe // For pre-populating the form in edit mode.
}

// ViewRecipesPageData holds data for the recipe list page, including search results.
type ViewRecipesPageData struct {
	SearchQuery string
	TagName     string
	Recipes     []db.Recipe
	CurrentPage int
	TotalPages  int
	NextPage    int
	PrevPage    int
	HasNext     bool
	HasPrev     bool
}

// MealPlanCookieData defines the structure for data stored in the meal plan cookie.
type MealPlanCookieData struct {
	Order  []string    `json:"order"` // Slice of recipe IDs, index corresponds to day of the week.
	Counts map[int]int `json:"counts"`
}

// MealPlanPageData holds data for the meal plan page.
type MealPlanPageData struct {
	AllRecipes   []db.RecipeInfo
	RecipeCounts map[int]int // Map of Recipe ID to its count in the meal plan
	ShoppingList []db.Ingredient
	MealOrder    []string
	SavedPlans   []db.MealPlanInfo
}

// ImportPageData holds data for the recipe import page.
type ImportPageData struct {
	Error    string
	JSONData string // To re-populate the textarea on error
}

// ParsePageData holds data for the AI recipe parsing page.
type ParsePageData struct {
	Error      string
	RecipeText string // To re-populate the textarea on error
	RecipeURL  string // To re-populate the URL field on error
}

const mealPlanCookieName = "meal-plan-recipes"
const unitSystemCookieName = "unit-system"
const recipesPerPage = 20

// allUnits is a list of all known measurement units, sorted by length descending.
// This is used for parsing ingredient strings. It is populated at startup.
var allUnits []string

func init() {
	// Populate allUnits from the conversion maps for ingredient parsing.
	for unit := range unitConversions {
		allUnits = append(allUnits, unit)
	}
	for unit := range weightConversions {
		// Avoid duplicates
		if _, exists := unitConversions[unit]; !exists {
			allUnits = append(allUnits, unit)
		}
	}
	// Sort by length descending to match longer units first (e.g., "fluid ounce" before "ounce").
	sort.Slice(allUnits, func(i, j int) bool {
		return len(allUnits[i]) > len(allUnits[j])
	})
}

// unitConversions maps various unit names to their equivalent value in the base unit (teaspoons).
var unitConversions = map[string]float64{
	"teaspoon":    1,
	"teaspoons":   1,
	"tsp":         1,
	"t":           1, // Common abbreviation
	"tablespoon":  3,
	"tablespoons": 3,
	"tbsp":        3,
	"T":           3, // Common abbreviation
	"fluid ounce": 6,
	"fl oz":       6,
	"cup":         48,
	"cups":        48,
	"c":           48, // Common abbreviation
	"pint":        96,
	"pints":       96,
	"pt":          96,
	"quart":       192,
	"quarts":      192,
	"qt":          192,
	"gallon":      768,
	"gallons":     768,
	"gal":         768,
	"milliliter":  0.202884,
	"milliliters": 0.202884,
	"ml":          0.202884,
	"liter":       202.884,
	"liters":      202.884,
	"l":           202.884,
}

// orderedUnits provides a hierarchy for formatting consolidated amounts into the largest practical unit.
var orderedUnits = []struct {
	Name  string
	Value float64 // Value in the base unit (teaspoons)
}{
	{"gallon", 768}, {"quart", 192}, {"pint", 96}, {"cup", 48}, {"tablespoon", 3}, {"teaspoon", 1},
}

// weightConversions maps various weight unit names to their equivalent value in the base unit (grams).
var weightConversions = map[string]float64{
	"gram":      1,
	"grams":     1,
	"g":         1,
	"ounce":     28.3495,
	"ounces":    28.3495,
	"oz":        28.3495,
	"pound":     453.592,
	"pounds":    453.592,
	"lb":        453.592,
	"lbs":       453.592,
	"kilogram":  1000,
	"kilograms": 1000,
	"kg":        1000,
}

// orderedWeightUnits provides a hierarchy for formatting consolidated weight amounts.
var orderedWeightUnits = []struct {
	Name  string
	Value float64 // Value in the base unit (grams)
}{
	{"kg", 1000}, {"pound", 453.592}, {"ounce", 28.3495}, {"gram", 1},
}

// orderedMetricVolumeUnits provides a hierarchy for formatting consolidated metric volume amounts.
var orderedMetricVolumeUnits = []struct {
	Name  string
	Value float64 // Value in base unit (teaspoons)
}{
	{"liter", 202.884},
	{"ml", 0.202884},
}

// orderedMetricWeightUnits provides a hierarchy for formatting consolidated metric weight amounts.
var orderedMetricWeightUnits = []struct {
	Name  string
	Value float64 // Value in base unit (grams)
}{
	{"kg", 1000},
	{"g", 1},
}

// JSONLDInstruction represents a step in a recipe's method, supporting multiple JSON-LD formats.
type JSONLDInstruction struct {
	Type string `json:"@type"`
	Text string `json:"text"`
}

// UnmarshalJSON allows JSONLDInstruction to correctly parse recipe steps
// that are either simple strings or structured "HowToStep" objects.
func (j *JSONLDInstruction) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		j.Type = "PlainText"
		j.Text = s
		return nil
	}

	// If it's not a string, unmarshal as a structured object
	type Alias JSONLDInstruction
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*j = JSONLDInstruction(a)
	return nil
}

// JSONLDIngredient represents an ingredient in JSON-LD, which can be a string or an object.
type JSONLDIngredient struct {
	Name   string
	Amount string
	Unit   string
}

// UnmarshalJSON allows JSONLDIngredient to be parsed from a simple string,
// or a structured object with a numeric or string quantity.
func (j *JSONLDIngredient) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a string first. This maintains backward compatibility
	// with the standard schema.org format.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed := parseIngredientString(s)
		j.Name = parsed.Name
		j.Amount = parsed.Amount
		j.Unit = parsed.Unit
		return nil
	}

	// If it's not a string, unmarshal as our custom structured object.
	// We use a temporary struct to avoid recursive calls to UnmarshalJSON.
	var a struct {
		Name     string      `json:"name"`
		Quantity interface{} `json:"quantity"`
		Unit     string      `json:"unit"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("ingredient must be a string or a structured object: %w", err)
	}

	j.Name = a.Name
	j.Unit = a.Unit

	// Handle flexible quantity type (string or number).
	switch v := a.Quantity.(type) {
	case string:
		j.Amount = v
	case float64:
		j.Amount = strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		j.Amount = ""
	default:
		// As a fallback, convert any other type to its string representation.
		j.Amount = fmt.Sprintf("%v", v)
	}

	return nil
}

// JSONLDRecipe represents the structure of a Recipe in JSON-LD format.
type JSONLDRecipe struct {
	Type               interface{}         `json:"@type"`
	Name               string              `json:"name"`
	URL                string              `json:"url"`
	RecipeIngredient   []JSONLDIngredient  `json:"recipeIngredient"`
	RecipeInstructions []JSONLDInstruction `json:"recipeInstructions"`
	Keywords           string              `json:"keywords"`
}

// --- Gemini API Structs ---

// GeminiRequestPart defines a part of the content for the Gemini API request.
type GeminiRequestPart struct {
	Text string `json:"text"`
}

// GeminiRequestContent defines the content for the Gemini API request.
type GeminiRequestContent struct {
	Parts []GeminiRequestPart `json:"parts"`
}

// GeminiRequest is the top-level structure for a request to the Gemini API.
type GeminiRequest struct {
	Contents []GeminiRequestContent `json:"contents"`
}

// GeminiResponsePart defines a part of the content in the Gemini API response.
type GeminiResponsePart struct {
	Text string `json:"text"`
}

// GeminiResponseContent defines the content in the Gemini API response.
type GeminiResponseContent struct {
	Parts []GeminiResponsePart `json:"parts"`
	Role  string               `json:"role"`
}

// GeminiCandidate holds a single response candidate from the Gemini API.
type GeminiCandidate struct {
	Content GeminiResponseContent `json:"content"`
}

// GeminiResponse is the top-level structure for a response from the Gemini API.
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

// --- End Gemini API Structs ---

func main() {
	// Initialize the database connection.
	err := db.InitDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.CloseDB()

	r := mux.NewRouter()

	// Static file server
	staticFileServer := http.FileServer(http.Dir("./web/static/"))
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", staticFileServer))

	// Page and action routes
	r.HandleFunc("/", logRequest(genericFileHandler("root.html"))).Methods("GET")
	r.HandleFunc("/new_page", logRequest(genericFileHandler("new_page.html"))).Methods("GET")
	r.HandleFunc("/submit_recipe", logRequest(submitRecipePageHandler)).Methods("GET")
	r.HandleFunc("/import-recipe", logRequest(importRecipePageHandler)).Methods("GET")
	r.HandleFunc("/parse-recipe", logRequest(parseRecipePageHandler)).Methods("GET")
	r.HandleFunc("/handle-parse", logRequest(handleParseRecipe)).Methods("POST")
	r.HandleFunc("/handle-import", logRequest(handleImportHandler)).Methods("POST")
	r.HandleFunc("/recipes", logRequest(viewRecipesHandler)).Methods("GET")
	r.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler)).Methods("POST")

	// Routes with path variables for recipe ID
	r.HandleFunc("/recipe/{id:[0-9]+}", logRequest(recipeDetailHandler)).Methods("GET")
	r.HandleFunc("/recipe/edit/{id:[0-9]+}", logRequest(editRecipePageHandler)).Methods("GET")
	r.HandleFunc("/recipe/update/{id:[0-9]+}", logRequest(updateRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipe/delete/{id:[0-9]+}", logRequest(deleteRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipe/duplicate/{id:[0-9]+}", logRequest(duplicateRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipes/tag/{tag}", logRequest(recipesByTagHandler)).Methods("GET")
	r.HandleFunc("/meal-plan", logRequest(mealPlanHandler)).Methods("GET", "POST")
	r.HandleFunc("/meal-plan/print", logRequest(printShoppingListHandler)).Methods("GET")
	r.HandleFunc("/meal-plan/save", logRequest(saveMealPlanHandler)).Methods("POST")
	r.HandleFunc("/meal-plan/load/{id:[0-9]+}", logRequest(loadMealPlanHandler)).Methods("GET")
	r.HandleFunc("/meal-plan/delete/{id:[0-9]+}", logRequest(deleteMealPlanHandler)).Methods("POST")
	r.HandleFunc("/meal-plan/set-units", logRequest(setUnitSystemHandler)).Methods("GET")
	// New API endpoint for AJAX updates
	r.HandleFunc("/meal-plan/api/update", logRequest(updateShoppingListAPIHandler)).Methods("POST")

	certFile := "certs/cert.pem"
	keyFile := "certs/key.pem"

	// Check if cert files exist before starting the server
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Fatalf("Certificate file not found: %s. Please run init-ssl.sh to generate it.", certFile)
	}

	fmt.Println("Starting web server with SSL on https://localhost:8443")
	err = http.ListenAndServeTLS(":8443", certFile, keyFile, r)
	if err != nil {
		log.Fatalf("Failed to start TLS server: %v", err)
	}
}

func setUnitSystemHandler(w http.ResponseWriter, r *http.Request) {
	system := r.URL.Query().Get("system")
	if system != "metric" && system != "imperial" {
		system = "imperial" // A safe default
	}

	http.SetCookie(w, &http.Cookie{
		Name:     unitSystemCookieName,
		Value:    system,
		Path:     "/",
		Expires:  time.Now().Add(365 * 24 * time.Hour), // Expires in 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})

	// Redirect back to the meal planner page. The browser will include the new cookie
	// in the subsequent GET request.
	http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
}

func recipesByTagHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tag := vars["tag"]

	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	offset := (page - 1) * recipesPerPage

	recipes, totalRecipes, err := db.FetchRecipesByTag(tag, recipesPerPage, offset)
	if err != nil {
		log.Printf("Error fetching recipes for tag %s: %v", tag, err)
		http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
		return
	}

	totalPages := (totalRecipes + recipesPerPage - 1) / recipesPerPage

	data := ViewRecipesPageData{
		TagName:     tag,
		Recipes:     recipes,
		CurrentPage: page,
		TotalPages:  totalPages,
		HasPrev:     page > 1,
		PrevPage:    page - 1,
		HasNext:     page < totalPages,
		NextPage:    page + 1,
	}

	renderTemplate(w, "web/recipes_by_tag.html", data)
}

func logRequest(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s\n", r.RemoteAddr, r.Method, r.URL)
		handler(w, r)
	}
}

func readFile(path_to_file string) (string, error) {
	data, err := os.ReadFile(path_to_file)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func genericFileHandler(filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileContent, err := readFile("web/" + filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			http.Error(w, "Failed to load content", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, fileContent)
	}
}

func submitRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	data := SubmitPageData{
		// Provide an empty Recipe struct for a new submission form.
		Recipe: &db.Recipe{},
	}

	tmpl, err := template.ParseFiles("web/submit_recipe.html")
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func editRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	recipe, err := db.FetchRecipeByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			log.Printf("Error fetching recipe %d for edit: %v", id, err)
			http.Error(w, "Failed to load recipe", http.StatusInternalServerError)
		}
		return
	}

	data := SubmitPageData{
		Recipe: recipe,
	}

	tmpl, err := template.ParseFiles("web/submit_recipe.html")
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func updateRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	title := r.PostFormValue("recipeTitle")
	sourceURL := r.PostFormValue("sourceURL")
	if title == "" {
		http.Error(w, "Recipe title is required", http.StatusBadRequest)
		return
	}

	ingredients, err := parseIngredientsForm(r)
	if err != nil {
		log.Printf("Error parsing ingredients form: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	method, err := parseMethodStepsForm(r)
	if err != nil {
		log.Printf("Error parsing method steps form: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tagsStr := r.PostFormValue("tags")
	var tags []string
	if tagsStr != "" {
		tags = strings.Split(tagsStr, ",")
		for i := range tags {
			tags[i] = strings.ToLower(strings.TrimSpace(tags[i]))
		}
	}

	servingsStr := r.PostFormValue("servings")
	servings, err := strconv.Atoi(servingsStr)
	if err != nil || servings <= 0 {
		servings = 4 // A safe default if parsing fails or value is invalid
	}

	if err := db.UpdateRecipe(id, title, sourceURL, servings, ingredients, method, tags); err != nil {
		log.Printf("Error updating recipe %d: %v", id, err)
		http.Error(w, "Failed to update recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully updated recipe %d.", id)
	// Redirect to the recipe detail page with a success message.
	http.Redirect(w, r, fmt.Sprintf("/recipe/%d?updated=true", id), http.StatusSeeOther)
}

func deleteRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := db.DeleteRecipeByID(id); err != nil {
		log.Printf("Error deleting recipe %d: %v", id, err)
		http.Error(w, "Failed to delete recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully deleted recipe %d.", id)
	// Redirect to the recipe list page.
	http.Redirect(w, r, "/recipes", http.StatusSeeOther)
}

func duplicateRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	newRecipeID, err := db.DuplicateRecipeByID(id)
	if err != nil {
		log.Printf("Error duplicating recipe %d: %v", id, err)
		http.Error(w, "Failed to duplicate recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully duplicated recipe %d into new recipe %d.", id, newRecipeID)
	// Redirect to the edit page of the new recipe.
	http.Redirect(w, r, fmt.Sprintf("/recipe/edit/%d", newRecipeID), http.StatusSeeOther)
}

func recipeDetailHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	recipe, err := db.FetchRecipeByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			log.Printf("Error fetching recipe %d: %v", id, err)
			http.Error(w, "Failed to load recipe", http.StatusInternalServerError)
		}
		return
	}

	isUpdated := r.URL.Query().Get("updated") == "true"
	isCreated := r.URL.Query().Get("created") == "true"
	data := struct {
		IsUpdated bool
		IsCreated bool
		Recipe    *db.Recipe
	}{
		IsUpdated: isUpdated,
		IsCreated: isCreated,
		Recipe:    recipe,
	}

	tmpl, err := template.ParseFiles("web/recipe_detail.html")
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func viewRecipesHandler(w http.ResponseWriter, r *http.Request) {
	// Get the search query from the URL, e.g., /recipes?q=chicken
	searchQuery := r.URL.Query().Get("q")

	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	offset := (page - 1) * recipesPerPage

	recipes, totalRecipes, err := db.FetchRecipes(searchQuery, recipesPerPage, offset)
	if err != nil {
		log.Printf("Error fetching recipes: %v", err)
		http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
		return
	}

	totalPages := (totalRecipes + recipesPerPage - 1) / recipesPerPage

	data := ViewRecipesPageData{
		SearchQuery: searchQuery,
		Recipes:     recipes,
		CurrentPage: page,
		TotalPages:  totalPages,
		HasPrev:     page > 1,
		PrevPage:    page - 1,
		HasNext:     page < totalPages,
		NextPage:    page + 1,
	}

	renderTemplate(w, "web/view_recipes.html", data)
}

// mealPlanHandler acts as a router, delegating to specific handlers based on the HTTP method.
func mealPlanHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet: // POST is now handled by the API endpoint
		handleViewMealPlan(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleViewMealPlan handles GET requests for the meal plan page.
func handleViewMealPlan(w http.ResponseWriter, r *http.Request) {
	// Handle clearing the meal plan
	if r.URL.Query().Get("action") == "clear" {
		clearMealPlanCookie(w, mealPlanCookieName)
		http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
		return
	}

	cookieData, err := getMealPlanFromCookie(r)
	if err != nil {
		log.Printf("Error reading meal plan cookie: %v. Clearing invalid cookie.", err)
		clearMealPlanCookie(w, mealPlanCookieName)
		http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
		return
	}

	recipeCounts := make(map[int]int)
	mealOrder := make([]string, 7)

	if cookieData != nil {
		recipeCounts = cookieData.Counts
		mealOrder = cookieData.Order
	}

	// Calculate total meal count
	totalMealCount := 0
	for _, count := range recipeCounts {
		totalMealCount += count
	}

	// Get user's preferred unit system from cookie, default to imperial
	unitSystemCookie, err := r.Cookie(unitSystemCookieName)
	unitSystem := "imperial"
	if err == nil {
		unitSystem = unitSystemCookie.Value
	}

	var shoppingList []db.Ingredient
	if len(recipeCounts) > 0 {
		shoppingList, err = consolidateIngredients(recipeCounts, unitSystem)
		if err != nil {
			log.Printf("Error consolidating ingredients: %v", err)
			http.Error(w, "Failed to generate shopping list", http.StatusInternalServerError)
			return
		}
	}

	allRecipes, err := db.FetchAllRecipeInfos()
	if err != nil {
		log.Printf("Error fetching all recipe infos: %v", err)
		http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
		return
	}

	// --- Pagination for Saved Plans ---
	const plansPerPage = 5
	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	offset := (page - 1) * plansPerPage

	totalPlans, err := db.CountSavedMealPlans()
	if err != nil {
		log.Printf("Error counting saved meal plans: %v", err)
		http.Error(w, "Failed to load saved plans", http.StatusInternalServerError)
		return
	}

	sortOrder := r.URL.Query().Get("sort")
	if sortOrder == "" {
		sortOrder = "name_asc" // Default sort order
	}

	savedPlans, err := db.ListSavedMealPlans(sortOrder, plansPerPage, offset)
	if err != nil {
		log.Printf("Error fetching saved meal plans: %v", err)
		// Non-fatal, just won't show the list
	}

	totalPages := (totalPlans + plansPerPage - 1) / plansPerPage

	data := struct {
		MealPlanPageData
		URL              *url.URL
		TotalMealCount   int
		UnitSystem       string
		SortOrder        string
		PlansCurrentPage int
		PlansTotalPages  int
		PlansHasNext     bool
		PlansHasPrev     bool
		PlansNextPage    int
		PlansPrevPage    int
	}{
		MealPlanPageData: MealPlanPageData{
			AllRecipes: allRecipes, RecipeCounts: recipeCounts, ShoppingList: shoppingList, MealOrder: mealOrder, SavedPlans: savedPlans,
		}, UnitSystem: unitSystem, TotalMealCount: totalMealCount,
		URL: r.URL, SortOrder: sortOrder, PlansCurrentPage: page, PlansTotalPages: totalPages, PlansHasNext: page < totalPages, PlansHasPrev: page > 1, PlansNextPage: page + 1, PlansPrevPage: page - 1,
	}
	renderMealPlanTemplate(w, data)
}

// printShoppingListHandler serves a printer-friendly version of the shopping list.
func printShoppingListHandler(w http.ResponseWriter, r *http.Request) {
	cookieData, err := getMealPlanFromCookie(r)
	if err != nil {
		// Log the error but continue; this will result in an empty list which is acceptable.
		log.Printf("Could not get meal plan from cookie for printing: %v", err)
	}

	recipeCounts := make(map[int]int)
	if cookieData != nil && cookieData.Counts != nil {
		recipeCounts = cookieData.Counts
	}

	unitSystemCookie, err := r.Cookie(unitSystemCookieName)
	unitSystem := "imperial"
	if err == nil {
		unitSystem = unitSystemCookie.Value
	}

	var shoppingList []db.Ingredient
	if len(recipeCounts) > 0 {
		var err error
		shoppingList, err = consolidateIngredients(recipeCounts, unitSystem)
		if err != nil {
			log.Printf("Error consolidating ingredients for printing: %v", err)
			http.Error(w, "Failed to generate shopping list", http.StatusInternalServerError)
			return
		}
	}

	// Use an anonymous struct for the template data, as we only need the list.
	data := struct {
		ShoppingList []db.Ingredient
	}{
		ShoppingList: shoppingList,
	}

	renderTemplate(w, "web/print_shopping_list.html", data)
}

func saveMealPlanHandler(w http.ResponseWriter, r *http.Request) {
	planName := r.PostFormValue("planName")
	if planName == "" {
		http.Error(w, "Plan name is required", http.StatusBadRequest)
		return
	}

	// Get current plan from cookie
	cookie, err := r.Cookie(mealPlanCookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "No active meal plan to save", http.StatusBadRequest)
		return
	}

	// The cookie value is base64 encoded JSON. We need to decode it before saving to the JSONB column.
	decodedCookieValue, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		http.Error(w, "Invalid meal plan data in cookie", http.StatusBadRequest)
		return
	}

	if err := db.SaveNamedMealPlan(planName, string(decodedCookieValue)); err != nil {
		log.Printf("Error saving named meal plan: %v", err)
		http.Error(w, "Failed to save meal plan", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/meal-plan?saved=true", http.StatusSeeOther)
}

func loadMealPlanHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	planDataJSON, err := db.LoadNamedMealPlan(id)
	if err != nil {
		log.Printf("Error loading named meal plan: %v", err)
		http.Error(w, "Failed to load meal plan", http.StatusInternalServerError)
		return
	}

	// The data from DB is JSON. We need to base64 encode it for the cookie.
	encodedValue := base64.StdEncoding.EncodeToString([]byte(planDataJSON))

	http.SetCookie(w, &http.Cookie{
		Name:     mealPlanCookieName,
		Value:    encodedValue,
		Path:     "/",
		Expires:  time.Now().Add(30 * 24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})

	http.Redirect(w, r, "/meal-plan?loaded=true", http.StatusSeeOther)
}

// updateShoppingListAPIHandler handles AJAX requests to update the shopping list.
func updateShoppingListAPIHandler(w http.ResponseWriter, r *http.Request) {
	var cookieData MealPlanCookieData
	if err := json.NewDecoder(r.Body).Decode(&cookieData); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Update the cookie in the user's browser for persistence.
	if err := setMealPlanCookie(w, &cookieData); err != nil {
		log.Printf("Error setting meal plan cookie during API update: %v", err)
		http.Error(w, "Failed to save meal plan state", http.StatusInternalServerError)
		return
	}

	unitSystemCookie, err := r.Cookie(unitSystemCookieName)
	unitSystem := "imperial"
	if err == nil {
		unitSystem = unitSystemCookie.Value
	}

	shoppingList, err := consolidateIngredients(cookieData.Counts, unitSystem)
	if err != nil {
		log.Printf("Error consolidating ingredients for API update: %v", err)
		http.Error(w, "Failed to generate shopping list", http.StatusInternalServerError)
		return
	}

	totalMealCount := 0
	for _, count := range cookieData.Counts {
		totalMealCount += count
	}

	type apiResponse struct {
		ShoppingList   []db.Ingredient `json:"shoppingList"`
		TotalMealCount int             `json:"totalMealCount"`
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiResponse{ShoppingList: shoppingList, TotalMealCount: totalMealCount})
}

// handleUpdateMealPlan handles POST requests to update the meal plan cookie.
func handleUpdateMealPlan(w http.ResponseWriter, r *http.Request) {
	cookieData, err := parseMealPlanForm(r)
	if err != nil {
		log.Printf("Error parsing meal plan form: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := setMealPlanCookie(w, cookieData); err != nil {
		log.Printf("Error setting meal plan cookie: %v", err)
		http.Error(w, "Failed to save meal plan", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
}

func deleteMealPlanHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	if err := db.DeleteNamedMealPlan(id); err != nil {
		log.Printf("Error deleting named meal plan: %v", err)
		http.Error(w, "Failed to delete meal plan", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/meal-plan?deleted=true", http.StatusSeeOther)
}

// getMealPlanFromCookie reads, decodes, and unmarshals the meal plan data from the cookie.
func getMealPlanFromCookie(r *http.Request) (*MealPlanCookieData, error) {
	cookie, err := r.Cookie(mealPlanCookieName)
	if err != nil {
		if err == http.ErrNoCookie {
			return nil, nil // No cookie is not an error, just no data.
		}
		return nil, err // Other errors are unexpected.
	}

	if cookie.Value == "" {
		return nil, nil // Empty cookie is also not an error.
	}

	decodedCookieValue, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, fmt.Errorf("could not decode meal plan cookie: %w", err)
	}

	var cookieData MealPlanCookieData
	if err := json.Unmarshal(decodedCookieValue, &cookieData); err != nil {
		return nil, fmt.Errorf("could not unmarshal meal plan cookie: %w", err)
	}

	// Ensure slices/maps are not nil to avoid panics later.
	if cookieData.Counts == nil {
		cookieData.Counts = make(map[int]int)
	}
	if cookieData.Order == nil || len(cookieData.Order) != 7 {
		cookieData.Order = make([]string, 7)
	}

	return &cookieData, nil
}

// setMealPlanCookie marshals, encodes, and sets the meal plan data in a cookie.
func setMealPlanCookie(w http.ResponseWriter, data *MealPlanCookieData) error {
	cookieJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("error marshalling meal plan cookie data: %w", err)
	}

	encodedCookieValue := base64.StdEncoding.EncodeToString(cookieJSON)

	http.SetCookie(w, &http.Cookie{
		Name:     mealPlanCookieName,
		Value:    encodedCookieValue,
		Path:     "/",
		Expires:  time.Now().Add(30 * 24 * time.Hour), // Expires in 30 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
	return nil
}

// parseMealPlanForm extracts recipe counts and order from the POST form.
func parseMealPlanForm(r *http.Request) (*MealPlanCookieData, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("failed to parse form: %w", err)
	}

	cookieData := &MealPlanCookieData{
		Counts: make(map[int]int),
		Order:  make([]string, 7),
	}

	re := regexp.MustCompile(`^count_(\d+)$`)
	for key, values := range r.PostForm {
		if len(values) == 0 {
			continue
		}
		if matches := re.FindStringSubmatch(key); len(matches) == 2 {
			recipeID, _ := strconv.Atoi(matches[1])
			count, err := strconv.Atoi(values[0])
			if err == nil && count > 0 {
				cookieData.Counts[recipeID] = count
			}
		}
	}

	orderParts := strings.Split(r.PostFormValue("meal_order"), ",")
	for i := 0; i < 7; i++ {
		if i < len(orderParts) && orderParts[i] != "" {
			recipeID, _ := strconv.Atoi(orderParts[i])
			if _, ok := cookieData.Counts[recipeID]; ok {
				cookieData.Order[i] = orderParts[i]
			}
		}
	}
	return cookieData, nil
}

func clearMealPlanCookie(w http.ResponseWriter, cookieName string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

func importRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "web/import_recipe.html", ImportPageData{})
}

func handleImportHandler(w http.ResponseWriter, r *http.Request) {
	jsonStr := r.PostFormValue("recipeJson")
	if jsonStr == "" {
		renderTemplate(w, "web/import_recipe.html", ImportPageData{
			Error:    "JSON data is required.",
			JSONData: jsonStr,
		})
		return
	}

	var recipe *db.Recipe
	var ldData []JSONLDRecipe
	// JSON-LD can be a single object or an array of objects. Try array first.
	if err := json.Unmarshal([]byte(jsonStr), &ldData); err == nil {
		for _, item := range ldData {
			if isRecipeType(item.Type) {
				recipe = convertJSONLDToRecipe(item)
				break // Found a recipe, stop searching
			}
		}
	} else {
		// If array fails, try a single object.
		var singleLDData JSONLDRecipe
		if err := json.Unmarshal([]byte(jsonStr), &singleLDData); err == nil {
			if isRecipeType(singleLDData.Type) {
				recipe = convertJSONLDToRecipe(singleLDData)
			}
		} else {
			// If both fail, it's invalid JSON
			log.Printf("Error unmarshalling imported JSON: %v", err)
			renderTemplate(w, "web/import_recipe.html", ImportPageData{
				Error:    "Invalid JSON format. Please ensure you are pasting valid JSON-LD.",
				JSONData: jsonStr,
			})
			return
		}
	}

	if recipe == nil {
		log.Printf("No recipe object found in the provided JSON")
		renderTemplate(w, "web/import_recipe.html", ImportPageData{
			Error:    "Could not find a valid recipe object in the provided JSON.",
			JSONData: jsonStr,
		})
		return
	}

	// Extract tags from the recipe struct to pass to SaveRecipe
	var tags []string
	for _, cat := range recipe.Categories {
		tags = append(tags, cat.Name)
	}

	// Save the parsed recipe to the database.
	newRecipeID, err := db.SaveRecipe(recipe.Title, recipe.SourceURL.String, recipe.Servings, recipe.Ingredients, recipe.Method, tags)
	if err != nil {
		log.Printf("Error saving imported recipe to database: %v", err)
		renderTemplate(w, "web/import_recipe.html", ImportPageData{
			Error:    "Failed to save imported recipe.",
			JSONData: jsonStr,
		})
		return
	}

	log.Printf("Successfully imported recipe '%s' into new recipe %d.", recipe.Title, newRecipeID)
	// Redirect to the edit page of the new recipe.
	http.Redirect(w, r, fmt.Sprintf("/recipe/edit/%d", newRecipeID), http.StatusSeeOther)
}

// isRecipeType checks if a JSON-LD object's type field indicates it's a recipe.
func isRecipeType(t interface{}) bool {
	if typeStr, ok := t.(string); ok {
		return strings.Contains(typeStr, "Recipe")
	}
	if typeArr, ok := t.([]interface{}); ok {
		for _, item := range typeArr {
			if str, ok := item.(string); ok && strings.Contains(str, "Recipe") {
				return true
			}
		}
	}
	return false
}

// convertJSONLDToRecipe transforms a scraped JSON-LD recipe into our internal Recipe struct.
func convertJSONLDToRecipe(ld JSONLDRecipe) *db.Recipe {
	recipe := &db.Recipe{
		Title:     ld.Name,
		SourceURL: sql.NullString{String: ld.URL, Valid: ld.URL != ""},
		Servings:  4, // Set a default serving size for imported recipes
	}

	for _, ing := range ld.RecipeIngredient {
		recipe.Ingredients = append(recipe.Ingredients, db.Ingredient{
			Name:   strings.Title(strings.ToLower(strings.TrimSpace(ing.Name))),
			Amount: ing.Amount,
			Unit:   strings.ToLower(strings.TrimSpace(ing.Unit)),
		})
	}

	for i, inst := range ld.RecipeInstructions {
		recipe.Method = append(recipe.Method, db.MethodStep{
			StepNumber:  i + 1,
			Description: strings.TrimSpace(inst.Text),
		})
	}

	if ld.Keywords != "" {
		tags := strings.Split(ld.Keywords, ",")
		for _, tag := range tags {
			recipe.Categories = append(recipe.Categories, db.Category{Name: strings.ToLower(strings.TrimSpace(tag))})
		}
	}
	return recipe
}

// fetchURLContent makes a GET request to a URL and returns its body as a string.
func fetchURLContent(urlStr string) (string, error) {
	// Basic validation
	_, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid URL format")
	}

	client := &http.Client{
		Timeout: 15 * time.Second, // Set a reasonable timeout
	}

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("could not create request: %w", err)
	}
	// Set a user-agent to mimic a browser, as some sites block default Go user-agents.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non-200 status code: %s", resp.Status)
	}

	// Limit the size of the body to prevent processing excessively large files.
	limitedReader := &io.LimitedReader{R: resp.Body, N: 2 * 1024 * 1024} // 2MB limit

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// parseRecipePageHandler serves the page where users can paste recipe text for AI parsing.
func parseRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "web/parse_recipe.html", ParsePageData{})
}

// handleParseRecipe takes raw text, sends it to the Gemini API, and pre-populates the recipe form.
func handleParseRecipe(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" || apiKey == "your_gemini_api_key_here" {
		log.Println("GEMINI_API_KEY is not set.")
		renderTemplate(w, "web/parse_recipe.html", ParsePageData{
			Error: "AI parsing is not configured on the server. Please set the GEMINI_API_KEY.",
		})
		return
	}

	recipeURL := r.PostFormValue("recipeURL")
	recipeText := r.PostFormValue("recipeText")
	var textToParse string
	var err error

	// This struct will be used to re-populate the form if any error occurs.
	pageDataOnError := ParsePageData{
		RecipeURL:  recipeURL,
		RecipeText: recipeText,
	}

	if recipeURL != "" {
		// User provided a URL, fetch its content.
		log.Printf("Parsing recipe from URL: %s", recipeURL)
		textToParse, err = fetchURLContent(recipeURL)
		if err != nil {
			log.Printf("Error fetching URL content: %v", err)
			pageDataOnError.Error = fmt.Sprintf("Failed to fetch content from URL: %v", err)
			renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
			return
		}
	} else if recipeText != "" {
		// User provided raw text.
		textToParse = recipeText
	} else {
		pageDataOnError.Error = "Please provide a URL or paste recipe text."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}

	var prompt string
	if recipeURL != "" {
		prompt = `
You are a culinary assistant. Your task is to parse a recipe from the provided HTML content and return it as a structured JSON object.
Find the main recipe content within the HTML and ignore ads, comments, and other irrelevant parts.

The JSON object must have the following fields: "title" (string), "sourceURL" (string, if found), "servings" (integer, if found), "ingredients" (array of objects), "method" (array of strings), and "tags" (array of strings).
- "ingredients" objects must have "name" (string), "amount" (string), and "unit" (string).
- "method" should be an array of strings, with each string being a single step.
- "tags" should be an array of relevant lowercase strings.

Do not include any explanations, markdown formatting, or introductory text. Only output the raw JSON object.

Here is the HTML content:
---
` + textToParse + `
---`
	} else {
		prompt = `
You are a culinary assistant. Your task is to parse unstructured recipe text and return it as a structured JSON object.

The JSON object must have the following fields: "title" (string), "sourceURL" (string, if found), "servings" (integer, if found), "ingredients" (array of objects), "method" (array of strings), and "tags" (array of strings).
- "ingredients" objects must have "name" (string), "amount" (string), and "unit" (string).
- "method" should be an array of strings, with each string being a single step.
- "tags" should be an array of relevant lowercase strings.

Do not include any explanations, markdown formatting, or introductory text. Only output the raw JSON object.

Here is the recipe text:
---
` + textToParse + `
---`
	}

	// Prepare the request to the Gemini API
	geminiReqBody := GeminiRequest{
		Contents: []GeminiRequestContent{
			{Parts: []GeminiRequestPart{{Text: prompt}}},
		},
	}
	reqBytes, err := json.Marshal(geminiReqBody)
	if err != nil {
		log.Printf("Error marshalling Gemini request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Make the API call
	url := "https://generativelanguage.googleapis.com/v1/models/gemini-2.5-flash:generateContent?key=" + apiKey
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBytes))
	if err != nil {
		log.Printf("Error calling Gemini API: %v", err)
		pageDataOnError.Error = "Failed to communicate with AI service."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Gemini API returned non-200 status: %s", resp.Status)
		pageDataOnError.Error = "AI service returned an error."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}

	// Decode the response
	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		pageDataOnError.Error = "Failed to parse AI response."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		pageDataOnError.Error = "AI returned an empty response."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}

	// The actual JSON content is a string within the response.
	// It's often wrapped in markdown backticks, which we need to remove before parsing.
	parsedContentJSON := geminiResp.Candidates[0].Content.Parts[0].Text
	parsedContentJSON = strings.TrimPrefix(parsedContentJSON, "```json")
	parsedContentJSON = strings.TrimPrefix(parsedContentJSON, "```")
	parsedContentJSON = strings.TrimSuffix(parsedContentJSON, "```")
	parsedContentJSON = strings.TrimSpace(parsedContentJSON)

	var parsedRecipe struct {
		Title       string           `json:"title"`
		SourceURL   string           `json:"sourceURL"`
		Servings    int              `json:"servings"`
		Ingredients []db.Ingredient  `json:"ingredients"`
		Method      []string         `json:"method"`
		Tags        []string         `json:"tags"`
	}
	if err := json.Unmarshal([]byte(parsedContentJSON), &parsedRecipe); err != nil {
		log.Printf("Error unmarshalling parsed recipe JSON from Gemini: %v", err)
		pageDataOnError.Error = "AI returned data in an unexpected format. Please try again."
		renderTemplate(w, "web/parse_recipe.html", pageDataOnError)
		return
	}

	// Prioritize the user-entered URL as the source.
	finalSourceURL := parsedRecipe.SourceURL
	if recipeURL != "" {
		finalSourceURL = recipeURL
	}
	// Convert the parsed data into our db.Recipe struct to pre-populate the form.
	recipeForForm := &db.Recipe{
		Title:     parsedRecipe.Title,
		SourceURL: sql.NullString{String: finalSourceURL, Valid: finalSourceURL != ""},
		Servings:  parsedRecipe.Servings,
		Ingredients: parsedRecipe.Ingredients,
	}
	for i, stepDesc := range parsedRecipe.Method {
		recipeForForm.Method = append(recipeForForm.Method, db.MethodStep{StepNumber: i + 1, Description: stepDesc})
	}
	for _, tagName := range parsedRecipe.Tags {
		recipeForForm.Categories = append(recipeForForm.Categories, db.Category{Name: tagName})
	}

	// Render the submission form with the pre-populated data.
	renderTemplate(w, "web/submit_recipe.html", SubmitPageData{Recipe: recipeForForm})
}

// normalizeAmount converts a string amount (potentially a fraction) into a clean float string.
func normalizeAmount(amountStr string) string {
	amountStr = strings.TrimSpace(amountStr)
	// Handle mixed numbers like "1 1/2"
	if strings.Contains(amountStr, " ") && strings.Contains(amountStr, "/") {
		parts := strings.Split(amountStr, " ")
		if len(parts) == 2 {
			whole, err1 := strconv.ParseFloat(parts[0], 64)
			fracParts := strings.Split(parts[1], "/")
			if len(fracParts) == 2 {
				num, err2 := strconv.ParseFloat(fracParts[0], 64)
				den, err3 := strconv.ParseFloat(fracParts[1], 64)
				if err1 == nil && err2 == nil && err3 == nil && den != 0 {
					return strconv.FormatFloat(whole+num/den, 'f', -1, 64)
				}
			}
		}
	}
	// Handle fractions like "1/2"
	if strings.Contains(amountStr, "/") && !strings.Contains(amountStr, " ") {
		fracParts := strings.Split(amountStr, "/")
		if len(fracParts) == 2 {
			num, err1 := strconv.ParseFloat(fracParts[0], 64)
			den, err2 := strconv.ParseFloat(fracParts[1], 64)
			if err1 == nil && err2 == nil && den != 0 {
				return strconv.FormatFloat(num/den, 'f', -1, 64)
			}
		}
	}
	// It's probably a decimal or integer already, just return it.
	return amountStr
}

// parseIngredientString attempts to split a raw ingredient string into amount, measurement, and name.
func parseIngredientString(ingStr string) db.Ingredient {
	// Regex to find a leading number, including simple fractions and mixed numbers.
	// e.g., "2", "1.5", "1/2", "1 1/2"
	re := regexp.MustCompile(`^(\d+\s+\d/\d|\d+/\d|\d*\.\d+|\d+)`)
	originalStr := strings.TrimSpace(ingStr)

	amountMatch := re.FindString(originalStr)

	if amountMatch == "" {
		// No numeric amount found, the whole string is the name.
		return db.Ingredient{Name: originalStr}
	}

	amountStr := normalizeAmount(amountMatch)
	remainingStr := strings.TrimSpace(originalStr[len(amountMatch):])

	// Check for a known unit of measurement.
	for _, unit := range allUnits {
		// Check for "cup " or an exact match "cup"
		if strings.HasPrefix(strings.ToLower(remainingStr), unit+" ") || strings.ToLower(remainingStr) == unit {
			unitName := unit
			name := strings.TrimSpace(remainingStr[len(unit):])
			// Clean up common leading characters like "of"
			name = strings.TrimPrefix(name, "of ")
			return db.Ingredient{
				Amount: amountStr,
				Unit:   unitName,
				Name:   name,
			}
		}
	}

	// No known unit found. The rest of the string is the name.
	// e.g., "2 large eggs" -> Amount: "2", Name: "large eggs"
	return db.Ingredient{
		Amount: amountStr,
		Unit:   "",
		Name:   remainingStr,
	}
}

func renderTemplate(w http.ResponseWriter, templateFile string, data interface{}) {
	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		log.Printf("Error parsing template %s: %v", templateFile, err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing template %s: %v", templateFile, err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func renderMealPlanTemplate(w http.ResponseWriter, data interface{}) {
	tmpl, err := template.ParseFiles("web/meal_plan.html")
	if err != nil {
		log.Printf("Error parsing meal plan template: %v", err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing meal plan template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

// ingredientAggregator holds the consolidated data for a shopping list.
type ingredientAggregator struct {
	// volumes holds ingredients convertible to a base volume unit (teaspoons).
	// map[IngredientName]TotalInBaseUnit
	volumes map[string]float64
	// weights holds ingredients convertible to a base weight unit (grams).
	// map[IngredientName]TotalInBaseUnit
	weights map[string]float64
	// others holds ingredients with numeric amounts but non-convertible units (e.g., "2 cloves").
	// map[IngredientName]map[Unit]Amount
	others map[string]map[string]float64
	// nonNumeric holds ingredients with non-numeric amounts (e.g., "a pinch").
	nonNumeric []db.Ingredient
}

// newIngredientAggregator creates and initializes an ingredientAggregator.
func newIngredientAggregator() *ingredientAggregator {
	return &ingredientAggregator{
		volumes:    make(map[string]float64),
		weights:    make(map[string]float64),
		others:     make(map[string]map[string]float64),
		nonNumeric: []db.Ingredient{},
	}
}

// add processes a single ingredient and adds it to the appropriate category in the aggregator.
func (a *ingredientAggregator) add(ing db.Ingredient, multiplier int) {
	name := strings.ToLower(strings.TrimSpace(ing.Name))
	unit := strings.ToLower(strings.TrimSpace(ing.Unit))

	amount, err := strconv.ParseFloat(ing.Amount, 64)
	if err != nil {
		// Amount is not a number (e.g., "a pinch", "to taste").
		// Add it to the list for each instance in the plan.
		for i := 0; i < multiplier; i++ {
			a.nonNumeric = append(a.nonNumeric, ing)
		}
		return
	}

	totalAmount := amount * float64(multiplier)

	// Check for convertible volume units.
	if factor, ok := unitConversions[unit]; ok {
		a.volumes[name] += totalAmount * factor
		return
	}

	// Check for convertible weight units.
	if factor, ok := weightConversions[unit]; ok {
		a.weights[name] += totalAmount * factor
		return
	}

	// Handle other numeric units (e.g., "cloves", "slices").
	if _, ok := a.others[name]; !ok {
		a.others[name] = make(map[string]float64)
	}
	a.others[name][ing.Unit] += totalAmount
}

// buildShoppingList compiles the aggregated data into a final, sorted shopping list.
func (a *ingredientAggregator) buildShoppingList(unitSystem string) []db.Ingredient {
	var shoppingList []db.Ingredient

	// Process convertible volume ingredients.
	for name, totalTeaspoons := range a.volumes {
		var amountStr, unitStr string
		if unitSystem == "metric" {
			amountStr, unitStr = formatAmountMetric(totalTeaspoons)
		} else {
			amountStr, unitStr = formatAmount(totalTeaspoons)
		}

		shoppingList = append(shoppingList, db.Ingredient{
			Name:   strings.Title(name),
			Amount: amountStr,
			Unit:   unitStr,
		})
	}

	// Process convertible weight ingredients.
	for name, totalGrams := range a.weights {
		var amountStr, unitStr string
		if unitSystem == "metric" {
			amountStr, unitStr = formatWeightAmountMetric(totalGrams)
		} else {
			amountStr, unitStr = formatWeightAmount(totalGrams)
		}

		shoppingList = append(shoppingList, db.Ingredient{
			Name:   strings.Title(name),
			Amount: amountStr,
			Unit:   unitStr,
		})
	}

	// Process other numeric ingredients.
	for name, unitMap := range a.others {
		for unit, amount := range unitMap {
			shoppingList = append(shoppingList, db.Ingredient{
				Name:   strings.Title(name),
				Unit:   unit,
				Amount: strconv.FormatFloat(amount, 'f', -1, 64),
			})
		}
	}

	// Add the non-numeric ingredients.
	shoppingList = append(shoppingList, a.nonNumeric...)

	// Sort the final list for consistent display.
	sort.Slice(shoppingList, func(i, j int) bool {
		if shoppingList[i].Name != shoppingList[j].Name {
			return shoppingList[i].Name < shoppingList[j].Name
		}
		return shoppingList[i].Unit < shoppingList[j].Unit
	})

	return shoppingList
}

// consolidateIngredients fetches ingredients for selected recipes and consolidates them into a shopping list.
func consolidateIngredients(recipeCounts map[int]int, unitSystem string) ([]db.Ingredient, error) {
	aggregator := newIngredientAggregator()

	for id, countMultiplier := range recipeCounts {
		recipe, err := db.FetchRecipeByID(id)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Printf("Skipping non-existent recipe ID %d in meal plan", id)
				continue
			}
			return nil, fmt.Errorf("failed to fetch recipe %d: %w", id, err)
		}

		for _, ing := range recipe.Ingredients {
			aggregator.add(ing, countMultiplier)
		}
	}

	return aggregator.buildShoppingList(unitSystem), nil
}

// formatAmount converts a total amount in a base unit (teaspoons) to a human-readable
// amount and unit (e.g., 48 tsp -> "1", "cup").
func formatAmount(totalTeaspoons float64) (string, string) {
	// For very small amounts, show them as fractions of a teaspoon.
	if totalTeaspoons < 1.0 {
		if totalTeaspoons >= 0.74 && totalTeaspoons < 0.76 {
			return "3/4", "teaspoon"
		}
		if totalTeaspoons >= 0.49 && totalTeaspoons < 0.51 {
			return "1/2", "teaspoon"
		}
		if totalTeaspoons >= 0.24 && totalTeaspoons < 0.26 {
			return "1/4", "teaspoon"
		}
		return strconv.FormatFloat(totalTeaspoons, 'f', 2, 64), "teaspoons"
	}

	for _, unit := range orderedUnits {
		// Check if the total is large enough to be represented in this unit.
		if totalTeaspoons >= unit.Value {
			amountInUnit := totalTeaspoons / unit.Value

			// Format to a reasonable number of decimal places, then clean it up.
			formattedAmount := strconv.FormatFloat(amountInUnit, 'f', 2, 64)
			formattedAmount = strings.TrimSuffix(formattedAmount, ".00")
			formattedAmount = strings.TrimSuffix(formattedAmount, ".0")

			unitName := unit.Name
			// Check for pluralization, avoiding floating point comparison issues.
			if amountInUnit > 1.001 {
				unitName += "s"
			}
			return formattedAmount, unitName
		}
	}

	// This should not be reached if orderedUnits includes "teaspoon", but it's a good fallback.
	return strconv.FormatFloat(totalTeaspoons, 'f', -1, 64), "teaspoons"
}

// formatAmountMetric converts a total amount in a base unit (teaspoons) to a human-readable
// metric amount and unit (e.g., 203 tsp -> "1", "liter").
func formatAmountMetric(totalTeaspoons float64) (string, string) {
	totalML := totalTeaspoons / unitConversions["ml"]

	for _, unit := range orderedMetricVolumeUnits {
		if totalTeaspoons >= unit.Value {
			amountInUnit := totalTeaspoons / unit.Value

			// Format to a reasonable number of decimal places, then clean it up.
			formattedAmount := strconv.FormatFloat(amountInUnit, 'f', 2, 64)
			formattedAmount = strings.TrimSuffix(formattedAmount, ".00")
			formattedAmount = strings.TrimSuffix(formattedAmount, ".0")

			return formattedAmount, unit.Name
		}
	}

	// Fallback for small amounts (less than 1 ml)
	formattedAmount := strconv.FormatFloat(totalML, 'f', 2, 64)
	return formattedAmount, "ml"
}

// formatWeightAmount converts a total amount in a base unit (grams) to a human-readable
// amount and unit (e.g., 1200g -> "1.2", "kg").
func formatWeightAmount(totalGrams float64) (string, string) {
	for _, unit := range orderedWeightUnits {
		// Use a small tolerance to handle floating point inaccuracies
		if totalGrams >= unit.Value-0.001 {
			amountInUnit := totalGrams / unit.Value

			// Format to a reasonable number of decimal places, then clean it up.
			formattedAmount := strconv.FormatFloat(amountInUnit, 'f', 2, 64)
			formattedAmount = strings.TrimSuffix(formattedAmount, ".00")
			formattedAmount = strings.TrimSuffix(formattedAmount, ".0")

			unitName := unit.Name
			// Check for pluralization, avoiding floating point comparison issues.
			// Special case for 'kg' which doesn't pluralize with 's'.
			if amountInUnit > 1.001 && unit.Name != "kg" {
				unitName += "s"
			}
			return formattedAmount, unitName
		}
	}

	// Fallback for values less than the smallest unit in the ordered list (e.g., < 1 gram).
	formattedAmount := strconv.FormatFloat(totalGrams, 'f', 2, 64)
	return formattedAmount, "g"
}

// formatWeightAmountMetric converts a total amount in grams to a human-readable metric amount.
func formatWeightAmountMetric(totalGrams float64) (string, string) {
	for _, unit := range orderedMetricWeightUnits {
		if totalGrams >= unit.Value {
			amountInUnit := totalGrams / unit.Value

			// Format to a reasonable number of decimal places, then clean it up.
			formattedAmount := strconv.FormatFloat(amountInUnit, 'f', 2, 64)
			formattedAmount = strings.TrimSuffix(formattedAmount, ".00")
			formattedAmount = strings.TrimSuffix(formattedAmount, ".0")

			return formattedAmount, unit.Name
		}
	}

	// Fallback for values less than the smallest unit in the ordered list (e.g., < 1 gram).
	// This will typically be 'g'.
	formattedAmount := strconv.FormatFloat(totalGrams, 'f', 2, 64)
	formattedAmount = strings.TrimSuffix(formattedAmount, ".00")
	return formattedAmount, "g"
}

func submitIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	title := r.PostFormValue("recipeTitle")
	if title == "" {
		http.Error(w, "Recipe title is required", http.StatusBadRequest)
		return
	}

	// The form sends data like `ingredients[0][name]`, `ingredients[1][amount]`, etc.
	// We need to parse this into a slice of Ingredient structs.
	ingredients, err := parseIngredientsForm(r)
	if err != nil {
		log.Printf("Error parsing ingredients form: %v", err)
		// Pass the specific validation error message to the user.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	method, err := parseMethodStepsForm(r)
	if err != nil {
		log.Printf("Error parsing method steps form: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tagsStr := r.PostFormValue("tags")
	var tags []string
	if tagsStr != "" {
		tags = strings.Split(tagsStr, ",")
		for i := range tags {
			tags[i] = strings.ToLower(strings.TrimSpace(tags[i]))
		}
	}

	servingsStr := r.PostFormValue("servings")
	servings, err := strconv.Atoi(servingsStr)
	if err != nil || servings <= 0 {
		servings = 4 // A safe default if parsing fails or value is invalid
	}

	sourceURL := r.PostFormValue("sourceURL")
	// Save the parsed ingredients to the database.
	recipeID, err := db.SaveRecipe(title, sourceURL, servings, ingredients, method, tags)
	if err != nil {
		log.Printf("Error saving recipe to database: %v", err)
		http.Error(w, "Failed to save recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully saved recipe '%s' with ID %d.", title, recipeID)

	// Redirect to the newly created recipe's detail page with a success message.
	http.Redirect(w, r, fmt.Sprintf("/recipe/%d?created=true", recipeID), http.StatusSeeOther)
}

// parseIngredientsForm extracts ingredient data from the HTTP request form.
func parseIngredientsForm(r *http.Request) ([]db.Ingredient, error) {
	// Regex to capture the index and field (name, amount, unit) from form keys.
	re := regexp.MustCompile(`^ingredients\[(\d+)\]\[(name|amount|unit)\]$`)
	ingredientsMap := make(map[int]db.Ingredient)

	for key, values := range r.PostForm {
		if len(values) == 0 {
			continue
		}

		matches := re.FindStringSubmatch(key)
		if len(matches) != 3 {
			continue
		}

		index, _ := strconv.Atoi(matches[1])
		field := matches[2]
		value := values[0]

		// Get or create the ingredient struct for this index
		ingredient := ingredientsMap[index]
		switch field {
		case "name":
			// Standardize to title case for consistent display.
			ingredient.Name = strings.Title(strings.ToLower(strings.TrimSpace(value)))
		case "amount":
			// Allow non-numeric amounts like "a pinch"
			ingredient.Amount = value
		case "unit":
			// Standardize to lower case for consistent matching.
			ingredient.Unit = strings.ToLower(strings.TrimSpace(value))
		}
		ingredientsMap[index] = ingredient
	}

	// Sort map keys to ensure ingredients are in the correct order.
	var keys []int
	for k := range ingredientsMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var ingredients []db.Ingredient
	for _, k := range keys {
		ingredients = append(ingredients, ingredientsMap[k])
	}

	return ingredients, nil
}

// parseMethodStepsForm extracts method step data from the HTTP request form.
func parseMethodStepsForm(r *http.Request) ([]db.MethodStep, error) {
	// Regex to capture the index from form keys like `method[0][description]`.
	re := regexp.MustCompile(`^method\[(\d+)\]\[description\]$`)
	stepsMap := make(map[int]string)

	for key, values := range r.PostForm {
		if len(values) == 0 || values[0] == "" {
			continue
		}

		matches := re.FindStringSubmatch(key)
		if len(matches) != 2 {
			continue
		}

		index, _ := strconv.Atoi(matches[1])
		stepsMap[index] = values[0]
	}

	// Sort map keys to ensure steps are in the correct order.
	var keys []int
	for k := range stepsMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var steps []db.MethodStep
	for i, k := range keys {
		steps = append(steps, db.MethodStep{
			StepNumber:  i + 1, // Ensure step numbers are sequential and 1-based.
			Description: stepsMap[k],
		})
	}

	return steps, nil
}
