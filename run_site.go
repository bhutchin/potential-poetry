package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"encoding/base64"
	"github.com/PuerkitoBio/goquery"
	"github.com/gorilla/mux"
	"potential-poetry/db"
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
	AllRecipes      []db.RecipeInfo
	RecipeCounts    map[int]int // Map of Recipe ID to its count in the meal plan
	ShoppingList    []db.Ingredient
	MealOrder       []string
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

// JSONLDRecipe represents the structure of a Recipe in JSON-LD format.
type JSONLDRecipe struct {
	Type               interface{}         `json:"@type"`
	Name               string              `json:"name"`
	RecipeIngredient   []string            `json:"recipeIngredient"`
	RecipeInstructions []JSONLDInstruction `json:"recipeInstructions"`
	Keywords           string              `json:"keywords"`
}

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
	r.HandleFunc("/handle-import", logRequest(handleImportHandler)).Methods("POST")
	r.HandleFunc("/recipes", logRequest(viewRecipesHandler)).Methods("GET")
	r.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler)).Methods("POST")

	// Routes with path variables for recipe ID
	r.HandleFunc("/recipe/{id:[0-9]+}", logRequest(recipeDetailHandler)).Methods("GET")
	r.HandleFunc("/recipe/edit/{id:[0-9]+}", logRequest(editRecipePageHandler)).Methods("GET")
	r.HandleFunc("/recipe/update/{id:[0-9]+}", logRequest(updateRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipe/delete/{id:[0-9]+}", logRequest(deleteRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipes/tag/{tag}", logRequest(recipesByTagHandler)).Methods("GET")
	r.HandleFunc("/meal-plan", logRequest(mealPlanHandler)).Methods("GET", "POST")

	fmt.Println("Starting web server..")
	err = http.ListenAndServe(":8080", r)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func recipesByTagHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tag := vars["tag"]

	const recipesPerPage = 5
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
	const importedRecipeCookieName = "imported-recipe"
	data := SubmitPageData{
		// Provide an empty Recipe struct for a new submission form.
		Recipe: &db.Recipe{},
	}

	// Check if we are loading an imported recipe from the cookie
	if r.URL.Query().Get("source") == "import" {
		if cookie, err := r.Cookie(importedRecipeCookieName); err == nil {
			// Clear the cookie immediately so it's only used once
			http.SetCookie(w, &http.Cookie{
				Name:     importedRecipeCookieName,
				Value:    "",
				Path:     "/",
				Expires:  time.Unix(0, 0),
				HttpOnly: true,
			})

			var recipe db.Recipe
			if err := json.Unmarshal([]byte(cookie.Value), &recipe); err == nil {
				data.Recipe = &recipe
				log.Printf("Loaded imported recipe '%s' for review.", recipe.Title)
			} else {
				log.Printf("Error unmarshalling imported recipe from cookie: %v", err)
			}
		}
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
			tags[i] = strings.TrimSpace(tags[i])
		}
	}

	if err := db.UpdateRecipe(id, title, ingredients, method, tags); err != nil {
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

	const recipesPerPage = 5
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

func mealPlanHandler(w http.ResponseWriter, r *http.Request) {
	const mealPlanCookieName = "meal-plan-recipes"

	// Handle clearing the meal plan
	if r.Method == "GET" && r.URL.Query().Get("action") == "clear" {
		http.SetCookie(w, &http.Cookie{
			Name:     mealPlanCookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
		})
		http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
		return
	}

	// Handle form submission to update the meal plan
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		cookieData := MealPlanCookieData{
			Counts: make(map[int]int),
			Order:  make([]string, 7), // 7 days
		}

		// Regex to find count inputs, e.g., "count_123"
		re := regexp.MustCompile(`^count_(\d+)$`)

		for key, values := range r.PostForm {
			if len(values) == 0 {
				continue
			}
			matches := re.FindStringSubmatch(key)
			if len(matches) == 2 {
				recipeIDStr := matches[1]
				countStr := values[0]
				recipeID, _ := strconv.Atoi(recipeIDStr)
				count, err := strconv.Atoi(countStr)
				if err != nil || count < 0 {
					continue // Ignore invalid or negative counts
				}

				if count > 0 {
					cookieData.Counts[recipeID] = count
				}
			}
		}

		// Process the meal_order input which contains the visual order of meals
		orderStr := r.PostFormValue("meal_order")
		// var orderParts []string
		if orderStr != "" {
			orderParts := strings.Split(orderStr, ",")

			// Ensure the order slice has exactly 7 elements
			for i := 0; i < 7; i++ {
				if i < len(orderParts) && orderParts[i] != "" {
					recipeIDStr := orderParts[i]
					recipeID, _ := strconv.Atoi(recipeIDStr)
					_, ok := cookieData.Counts[recipeID]
					if ok {
						cookieData.Order[i] = recipeIDStr //Only add to the order if the recipeId is in the counts.
					} else {
						cookieData.Order[i] = "" // set order to empty string as the recipe count doesnt exist.
					}
				} else {
					cookieData.Order[i] = "" // Ensure empty days are empty strings
				}
			}

			//Also populate the meal order from the counts, this ensures they are in both places.
		}

		// Process the meal_order input which contains the visual order of meals
		//if orderStr != "" {
		//	orderParts := strings.Split(orderStr, ",")
		//	// Ensure the order slice has exactly 7 elements
		//  for i := 0; i < 7; i++ {
		//		if i < len(orderParts) && orderParts[i] != "" {
		//  recipeIDStr := orderParts[i]
		//  _, err := strconv.Atoi(recipeIDStr)
		//    if err == nil {
		//      cookieData.Order[i] = recipeIDStr  //Only add to the order if the recipeId is in the counts.
		//    }
		//		} else {
		//			cookieData.Order[i] = "" // Ensure empty days are empty strings
		//		}
		//}

		// Marshal the JSON
		var err error

		cookieJSON, err := json.Marshal(cookieData)
		if err != nil {
			log.Printf("Error marshalling meal plan cookie data: %v", err)
			http.Error(w, "Failed to save meal plan", http.StatusInternalServerError)
			return
		}

		// Base64 encode the JSON data
		encodedCookieValue := base64.StdEncoding.EncodeToString(cookieJSON)

		http.SetCookie(w, &http.Cookie{
			Name:     mealPlanCookieName,
			Value:    encodedCookieValue,
			Path:     "/",
			Expires:  time.Now().Add(30 * 24 * time.Hour), // Expires in 30 days
			HttpOnly: true,
		})

		http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
		return
	}

	recipeCounts := make(map[int]int)
	mealOrder := make([]string, 7) // Default to 7 empty days

	if cookie, err := r.Cookie(mealPlanCookieName); err == nil && cookie.Value != "" {


		decodedCookieValue, err := base64.StdEncoding.DecodeString(cookie.Value)
		if(err != nil) {
			log.Printf("Could not decode meal plan cookie: %v. Clearing invalid cookie.", err)
			clearMealPlanCookie(w, mealPlanCookieName)
			http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
			return // Important to return after a redirect
		}
		var cookieData MealPlanCookieData
		if err := json.Unmarshal([]byte(decodedCookieValue), &cookieData); err == nil {
			if cookieData.Counts != nil {
				recipeCounts = cookieData.Counts
			}
			if len(cookieData.Order) == 7 {
				mealOrder = cookieData.Order
			}
		} else {
			log.Printf("Could not unmarshal meal plan cookie: %v. Clearing invalid cookie.", err)
			clearMealPlanCookie(w, mealPlanCookieName)
			http.Redirect(w, r, "/meal-plan", http.StatusSeeOther)
			return // Important to return after a redirect
		}
	}

	var shoppingList []db.Ingredient
	if len(recipeCounts) > 0 {
		var err error
		shoppingList, err = consolidateIngredients(recipeCounts)
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

	data := MealPlanPageData{
		AllRecipes:   allRecipes,
		RecipeCounts: recipeCounts,
		ShoppingList: shoppingList,
		MealOrder:    mealOrder,
	}
	renderMealPlanTemplate(w, data)
}

func clearMealPlanCookie(w http.ResponseWriter, cookieName string) {
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
		})
}

func importRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "web/import_recipe.html", nil)
}

func handleImportHandler(w http.ResponseWriter, r *http.Request) {
	const importedRecipeCookieName = "imported-recipe"
	url := r.PostFormValue("recipeUrl")
	if url == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	res, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching URL %s: %v", url, err)
		http.Error(w, "Failed to fetch the recipe URL.", http.StatusInternalServerError)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		log.Printf("URL %s returned status code %d", url, res.StatusCode)
		http.Error(w, fmt.Sprintf("Failed to fetch the recipe URL (status: %d).", res.StatusCode), http.StatusInternalServerError)
		return
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Printf("Error parsing HTML from %s: %v", url, err)
		http.Error(w, "Failed to parse the recipe page.", http.StatusInternalServerError)
		return
	}

	var recipe *db.Recipe
	doc.Find("script[type=\"application/ld+json\"]").EachWithBreak(func(i int, s *goquery.Selection) bool {
		var ldData []JSONLDRecipe
		// JSON-LD can be a single object or an array of objects. Try array first.
		if err := json.Unmarshal([]byte(s.Text()), &ldData); err == nil {
			for _, item := range ldData {
				if isRecipeType(item.Type) {
					recipe = convertJSONLDToRecipe(item)
					return false // Stop searching
				}
			}
		} else {
			// If array fails, try a single object.
			var singleLDData JSONLDRecipe
			if err := json.Unmarshal([]byte(s.Text()), &singleLDData); err == nil {
				if isRecipeType(singleLDData.Type) {
					recipe = convertJSONLDToRecipe(singleLDData)
					return false // Stop searching
				}
			}
		}
		return true // Continue searching
	})

	if recipe == nil {
		log.Printf("No recipe JSON-LD found on %s", url)
		http.Error(w, "Could not find a recipe on that page. This feature works best with sites that use modern recipe standards (JSON-LD).", http.StatusUnprocessableEntity)
		return
	}

	recipeJSON, err := json.Marshal(recipe)
	if err != nil {
		log.Printf("Error marshalling scraped recipe to JSON: %v", err)
		http.Error(w, "Failed to process scraped recipe.", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     importedRecipeCookieName,
		Value:    string(recipeJSON),
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute), // Short-lived cookie
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/submit_recipe?source=import", http.StatusSeeOther)
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
		Title: ld.Name,
	}

	for _, ingStr := range ld.RecipeIngredient {
		recipe.Ingredients = append(recipe.Ingredients, db.Ingredient{Name: ingStr})
	}

	for i, inst := range ld.RecipeInstructions {
		recipe.Method = append(recipe.Method, db.MethodStep{
			StepNumber:  i + 1,
			Description: inst.Text,
		})
	}

	if ld.Keywords != "" {
		tags := strings.Split(ld.Keywords, ",")
		for _, tag := range tags {
			recipe.Categories = append(recipe.Categories, db.Category{Name: strings.TrimSpace(tag)})
		}
	}
	return recipe
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

func renderMealPlanTemplate(w http.ResponseWriter, data MealPlanPageData) {
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

// consolidateIngredients fetches ingredients for selected recipes and consolidates them.
func consolidateIngredients(recipeCounts map[int]int) ([]db.Ingredient, error) {
	// map[IngredientName]map[Measurement]Amount
	consolidated := make(map[string]map[string]float64)
	var shoppingList []db.Ingredient

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
			amount, err := strconv.ParseFloat(ing.Amount, 64)
			if err != nil {
				// Could not parse amount, so we can't aggregate it.
				// Add it to the shopping list as-is for each time the recipe is included.
				for i := 0; i < countMultiplier; i++ {
					shoppingList = append(shoppingList, ing)
				}
				continue // Move to the next ingredient
			}

			totalAmount := amount * float64(countMultiplier)

			// Normalize name and measurement for better consolidation.
			name := strings.ToLower(strings.TrimSpace(ing.Name))
			measurement := strings.ToLower(strings.TrimSpace(ing.Measurement))

			// Simple singularization for common units (e.g., cups -> cup)
			if len(measurement) > 1 && strings.HasSuffix(measurement, "s") {
				measurement = strings.TrimSuffix(measurement, "s")
			}

			if _, ok := consolidated[name]; !ok {
				consolidated[name] = make(map[string]float64)
			}
			consolidated[name][measurement] += totalAmount
		}
	}

	for name, measurementMap := range consolidated {
		for measurement, amount := range measurementMap {
			shoppingList = append(shoppingList, db.Ingredient{
				Name:        strings.Title(name), // Capitalize for display
				Measurement: measurement,
				Amount:      strconv.FormatFloat(amount, 'f', -1, 64),
			})
		}
	}

	sort.Slice(shoppingList, func(i, j int) bool {
		if shoppingList[i].Name != shoppingList[j].Name {
			return shoppingList[i].Name < shoppingList[j].Name
		}
		return shoppingList[i].Measurement < shoppingList[j].Measurement
	})

	return shoppingList, nil
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
			tags[i] = strings.TrimSpace(tags[i])
		}
	}

	// Save the parsed ingredients to the database.
	recipeID, err := db.SaveRecipe(title, ingredients, method, tags)
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
	// Regex to capture the index and field (name, amount, measurement) from form keys.
	re := regexp.MustCompile(`^ingredients\[(\d+)\]\[(name|amount|measurement)\]$`)
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
			ingredient.Name = value
		case "amount":
			// Allow non-numeric amounts like "a pinch"
			ingredient.Amount = value
		case "measurement":
			ingredient.Measurement = value
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
