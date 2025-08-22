package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

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
	http.HandleFunc("/submit_recipe", logRequest(genericFileHandler("submit_recipe.html"))) // This now correctly points to the recipe form
	http.HandleFunc("/submit_ingredients", logRequest(submitIngredientsHandler))
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

func submitIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// The form sends data like `ingredients[0][name]`, `ingredients[1][amount]`, etc.
	// We need to parse this into a slice of Ingredient structs.
	ingredients, err := parseIngredientsForm(r)
	if err != nil {
		log.Printf("Error parsing ingredients form: %v", err)
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	// Save the parsed ingredients to the database.
	if err := saveRecipe(ingredients); err != nil {
		log.Printf("Error saving recipe to database: %v", err)
		http.Error(w, "Failed to save recipe", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully saved a recipe with %d ingredients.", len(ingredients))

	// Redirect the user back to the recipe page after submission.
	http.Redirect(w, r, "/submit_recipe", http.StatusSeeOther)
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
func saveRecipe(ingredients []Ingredient) error {
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // Rollback is a no-op if Commit succeeds.

	var recipeID int
	err = tx.QueryRow("INSERT INTO recipes DEFAULT VALUES RETURNING id").Scan(&recipeID)
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
