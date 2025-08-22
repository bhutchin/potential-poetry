package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

// DB is a global database connection pool.
var DB *sql.DB

// Ingredient represents the structure of a single ingredient.
type Ingredient struct {
	Name        string
	Amount      string
	Measurement string
}

// Recipe represents a recipe with its ingredients.
type Recipe struct {
	ID          int
	Title       string
	CreatedAt   time.Time
	Ingredients []Ingredient
}

// SubmitPageData holds data for the recipe submission page template.
type SubmitPageData struct {
	Success bool
	Error   string
	Recipe  *Recipe // For pre-populating the form in edit mode.
}

func main() {
	// Initialize the database connection.
	err := initDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer DB.Close()

	http.HandleFunc("/", logRequest(genericFileHandler("root.html")))
	http.HandleFunc("/new_page", logRequest(genericFileHandler("new_page.html")))
	http.HandleFunc("/static/", staticFileHandler)
	http.HandleFunc("/submit_recipe", logRequest(submitRecipePageHandler))
	http.HandleFunc("/recipe/edit/", logRequest(editRecipePageHandler))
	http.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler))
	http.HandleFunc("/recipe/update/", logRequest(updateRecipeHandler))
	http.HandleFunc("/recipes", logRequest(viewRecipesHandler))
	http.HandleFunc("/recipe/delete/", logRequest(deleteRecipeHandler))
	http.HandleFunc("/recipe/", logRequest(recipeDetailHandler))
	fmt.Println("Starting web server..")
	err = http.ListenAndServe(":8080", nil)
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

// initDB initializes the database connection using environment variables.
func initDB() error {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
	)

	var err error
	DB, err = sql.Open("pgx", connStr)
	if err != nil {
		return err
	}

	return DB.Ping()
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

func staticFileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/static/"):] // Strip "/static/" prefix
	if path == "" {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join("web", "static", path)
	http.ServeFile(w, r, fullPath)

	// content, err := os.ReadFile(fullPath)
	// if err != nil {
	// 	http.Error(w, "File not found", http.StatusNotFound)
	// 	return
	// }
	// w.Header().Set("Content-Type", "text/css")
	// w.Write(content)
}

func submitRecipePageHandler(w http.ResponseWriter, r *http.Request) {
	// Check for a success message from a redirect.
	success := r.URL.Query().Get("success") == "true"

	data := SubmitPageData{
		Success: success,
		// Provide an empty Recipe struct for a new submission form.
		Recipe:  &Recipe{},
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
	idStr := r.URL.Path[len("/recipe/edit/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	recipe, err := fetchRecipeByID(id)
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

	idStr := r.URL.Path[len("/recipe/update/"):]
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

	if err := updateRecipe(id, title, ingredients); err != nil {
		log.Printf("Error updating recipe %d: %v", id, err)
		http.Error(w, "Failed to update recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully updated recipe %d.", id)
	// Redirect to the recipe detail page with a success message.
	http.Redirect(w, r, fmt.Sprintf("/recipe/%d?success=true", id), http.StatusSeeOther)
}

func deleteRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from URL, e.g., "/recipe/delete/1" -> "1"
	idStr := r.URL.Path[len("/recipe/delete/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := deleteRecipeByID(id); err != nil {
		log.Printf("Error deleting recipe %d: %v", id, err)
		http.Error(w, "Failed to delete recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully deleted recipe %d.", id)
	// Redirect to the recipe list page.
	http.Redirect(w, r, "/recipes", http.StatusSeeOther)
}

func deleteRecipeByID(id int) error {
	// The ON DELETE CASCADE on the ingredients table will handle deleting associated ingredients.
	_, err := DB.Exec("DELETE FROM recipes WHERE id = $1", id)
	return err
}

func updateRecipe(id int, title string, ingredients []Ingredient) error {
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Update the recipe title
	_, err = tx.Exec("UPDATE recipes SET title = $1 WHERE id = $2", title, id)
	if err != nil {
		return fmt.Errorf("failed to update recipe title: %w", err)
	}

	// 2. Delete old ingredients
	_, err = tx.Exec("DELETE FROM ingredients WHERE recipe_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete old ingredients: %w", err)
	}

	// 3. Insert new ingredients
	stmt, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, measurement) VALUES($1, $2, $3, $4)")
	if err != nil {
		return fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmt.Close()

	for _, ing := range ingredients {
		if _, err := stmt.Exec(id, ing.Name, ing.Amount, ing.Measurement); err != nil {
			return fmt.Errorf("failed to insert ingredient %s: %w", ing.Name, err)
		}
	}

	return tx.Commit()
}

func recipeDetailHandler(w http.ResponseWriter, r *http.Request) {
	// Extract ID from URL, e.g., "/recipe/1" -> "1"
	idStr := r.URL.Path[len("/recipe/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	recipe, err := fetchRecipeByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			log.Printf("Error fetching recipe %d: %v", id, err)
			http.Error(w, "Failed to load recipe", http.StatusInternalServerError)
		}
		return
	}

	success := r.URL.Query().Get("success") == "true"
	data := struct {
		Success bool
		Recipe  *Recipe
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

func fetchRecipeByID(id int) (*Recipe, error) {
	query := `
		SELECT r.id, r.title, r.created_at, i.name, i.amount, i.measurement
		FROM recipes r
		LEFT JOIN ingredients i ON r.id = i.recipe_id
		WHERE r.id = $1
		ORDER BY i.id
	`
	rows, err := DB.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recipe *Recipe
	for rows.Next() {
		var recipeID int
		var recipeTitle string
		var recipeCreatedAt time.Time
		var ingName, ingAmount, ingMeasurement sql.NullString
		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &ingName, &ingAmount, &ingMeasurement); err != nil {
			return nil, err
		}
		if recipe == nil {
			recipe = &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt}
		}
		if ingName.Valid {
			recipe.Ingredients = append(recipe.Ingredients, Ingredient{Name: ingName.String, Amount: ingAmount.String, Measurement: ingMeasurement.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if recipe == nil {
		return nil, sql.ErrNoRows // No recipe found
	}
	return recipe, nil
}

func viewRecipesHandler(w http.ResponseWriter, r *http.Request) {
	recipes, err := fetchRecipes()
	if err != nil {
		log.Printf("Error fetching recipes: %v", err)
		http.Error(w, "Failed to load recipes", http.StatusInternalServerError)
		return
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
	err = tmpl.Execute(w, recipes)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func fetchRecipes() ([]Recipe, error) {
	// The query joins recipes and ingredients and orders them to make grouping in Go easier.
	query := `
		SELECT r.id, r.title, r.created_at, i.name, i.amount, i.measurement
		FROM recipes r
		LEFT JOIN ingredients i ON r.id = i.recipe_id
		ORDER BY r.created_at DESC, r.id, i.id
	`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Use a map to group ingredients by recipe ID efficiently.
	recipeMap := make(map[int]*Recipe)
	var orderedRecipes []*Recipe // Use a slice to maintain the order from the query.

	for rows.Next() {
		var recipeID int
		var recipeTitle string
		var recipeCreatedAt time.Time
		var ingName, ingAmount, ingMeasurement sql.NullString // Use sql.NullString for LEFT JOIN

		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &ingName, &ingAmount, &ingMeasurement); err != nil {
			return nil, err
		}

		if _, ok := recipeMap[recipeID]; !ok {
			recipe := &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt}
			recipeMap[recipeID] = recipe
			orderedRecipes = append(orderedRecipes, recipe)
		}

		if ingName.Valid {
			recipeMap[recipeID].Ingredients = append(recipeMap[recipeID].Ingredients, Ingredient{Name: ingName.String, Amount: ingAmount.String, Measurement: ingMeasurement.String})
		}
	}

	// Convert pointer slice to value slice for the template.
	recipes := make([]Recipe, len(orderedRecipes))
	for i, r := range orderedRecipes {
		recipes[i] = *r
	}

	return recipes, nil
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

	// Save the parsed ingredients to the database.
	if err := saveRecipe(title, ingredients); err != nil {
		log.Printf("Error saving recipe to database: %v", err)
		http.Error(w, "Failed to save recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully saved recipe '%s' with %d ingredients.", title, len(ingredients))

	// Redirect the user back to the recipe page after submission.
	http.Redirect(w, r, "/submit_recipe?success=true", http.StatusSeeOther)
}

// parseIngredientsForm extracts ingredient data from the HTTP request form.
func parseIngredientsForm(r *http.Request) ([]Ingredient, error) {
	// Regex to capture the index and field (name, amount, measurement) from form keys.
	re := regexp.MustCompile(`^ingredients\[(\d+)\]\[(name|amount|measurement)\]$`)
	ingredientsMap := make(map[int]Ingredient)

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

	var ingredients []Ingredient
	for _, k := range keys {
		ingredients = append(ingredients, ingredientsMap[k])
	}

	return ingredients, nil
}

// saveRecipe saves a new recipe and its ingredients to the database in a single transaction.
func saveRecipe(title string, ingredients []Ingredient) error {
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // Rollback is a no-op if Commit succeeds.

	var recipeID int
	err = tx.QueryRow("INSERT INTO recipes (title) VALUES ($1) RETURNING id", title).Scan(&recipeID)
	if err != nil {
		return fmt.Errorf("failed to create recipe: %w", err)
	}

	stmt, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, measurement) VALUES($1, $2, $3, $4)")
	if err != nil {
		return fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmt.Close()

	for _, ing := range ingredients {
		if _, err := stmt.Exec(recipeID, ing.Name, ing.Amount, ing.Measurement); err != nil {
			return fmt.Errorf("failed to insert ingredient %s: %w", ing.Name, err)
		}
	}

	return tx.Commit()
}
