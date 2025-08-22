package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"

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
	Recipes     []db.Recipe
	CurrentPage int
	TotalPages  int
	NextPage    int
	PrevPage    int
	HasNext     bool
	HasPrev     bool
}

// MealPlanPageData holds data for the meal plan page.
type MealPlanPageData struct {
	AllRecipes      []db.RecipeInfo
	SelectedRecipes map[int]bool
	ShoppingList    []db.Ingredient
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
	r.HandleFunc("/recipes", logRequest(viewRecipesHandler)).Methods("GET")
	r.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler)).Methods("POST")

	// Routes with path variables for recipe ID
	r.HandleFunc("/recipe/{id:[0-9]+}", logRequest(recipeDetailHandler)).Methods("GET")
	r.HandleFunc("/recipe/edit/{id:[0-9]+}", logRequest(editRecipePageHandler)).Methods("GET")
	r.HandleFunc("/recipe/update/{id:[0-9]+}", logRequest(updateRecipeHandler)).Methods("POST")
	r.HandleFunc("/recipe/delete/{id:[0-9]+}", logRequest(deleteRecipeHandler)).Methods("POST")
	r.HandleFunc("/meal-plan", logRequest(mealPlanHandler)).Methods("GET", "POST")

	fmt.Println("Starting web server..")
	err = http.ListenAndServe(":8080", r)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
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
	// Check for a success message from a redirect.
	success := r.URL.Query().Get("success") == "true"

	data := SubmitPageData{
		Success: success,
		// Provide an empty Recipe struct for a new submission form.
		Recipe:  &db.Recipe{},
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

	if err := db.UpdateRecipe(id, title, ingredients, method); err != nil {
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

	success := r.URL.Query().Get("updated") == "true"
	data := struct {
		Success bool
		Recipe  *db.Recipe
	}{
		Success: success,
		Recipe:  recipe,
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

	// Parse the template. Using ParseFiles is important for security and performance.
	tmpl, err := template.ParseFiles("web/view_recipes.html")
	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	// Execute the template with the data.
	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func mealPlanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Fetch all recipes for the selection list
		allRecipes, err := db.FetchAllRecipeInfos()
		if err != nil {
			log.Printf("Error fetching all recipe infos: %v", err)
			http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
			return
		}

		data := MealPlanPageData{
			AllRecipes: allRecipes,
		}
		renderMealPlanTemplate(w, data)
		return
	}

	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		recipeIDsStr := r.PostForm["recipe_ids"]
		var recipeIDs []int
		selectedRecipesMap := make(map[int]bool)
		for _, idStr := range recipeIDsStr {
			id, err := strconv.Atoi(idStr)
			if err == nil {
				recipeIDs = append(recipeIDs, id)
				selectedRecipesMap[id] = true
			}
		}

		// Consolidate ingredients
		shoppingList, err := consolidateIngredients(recipeIDs)
		if err != nil {
			log.Printf("Error consolidating ingredients: %v", err)
			http.Error(w, "Failed to generate shopping list", http.StatusInternalServerError)
			return
		}

		// Fetch all recipes again for rendering the page
		allRecipes, err := db.FetchAllRecipeInfos()
		if err != nil {
			log.Printf("Error fetching all recipe infos: %v", err)
			http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
			return
		}

		data := MealPlanPageData{
			AllRecipes:      allRecipes,
			SelectedRecipes: selectedRecipesMap,
			ShoppingList:    shoppingList,
		}
		renderMealPlanTemplate(w, data)
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
func consolidateIngredients(recipeIDs []int) ([]db.Ingredient, error) {
	// map[IngredientName]map[Measurement]Amount
	consolidated := make(map[string]map[string]float64)

	for _, id := range recipeIDs {
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
				log.Printf("Could not parse amount '%s' for ingredient '%s', skipping aggregation", ing.Amount, ing.Name)
				continue
			}

			if _, ok := consolidated[ing.Name]; !ok {
				consolidated[ing.Name] = make(map[string]float64)
			}
			consolidated[ing.Name][ing.Measurement] += amount
		}
	}

	var shoppingList []db.Ingredient
	for name, measurementMap := range consolidated {
		for measurement, amount := range measurementMap {
			shoppingList = append(shoppingList, db.Ingredient{
				Name:        name,
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

	// Save the parsed ingredients to the database.
	if err := db.SaveRecipe(title, ingredients, method); err != nil {
		log.Printf("Error saving recipe to database: %v", err)
		http.Error(w, "Failed to save recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully saved recipe '%s' with %d ingredients.", title, len(ingredients))

	// Redirect the user back to the recipe page after submission.
	http.Redirect(w, r, "/submit_recipe?success=true", http.StatusSeeOther)
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
			// Validate that the amount is a number before assigning it.
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				// The row number is index + 1 for a user-friendly message.
				return nil, fmt.Errorf("invalid amount in row %d: '%s' is not a valid number", index+1, value)
			}
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
