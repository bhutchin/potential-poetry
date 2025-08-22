package db

import (
	"database/sql"
	"fmt"
	"strings"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

// conn is the package-level database connection pool.
var conn *sql.DB

// Ingredient represents the structure of a single ingredient.
type Ingredient struct {
	Name        string
	Amount      string
	Measurement string
}

// MethodStep represents a single step in a recipe's method.
type MethodStep struct {
	StepNumber  int
	Description string
}

// Recipe represents a recipe with its ingredients.
type Recipe struct {
	ID          int
	Title       string
	CreatedAt   time.Time
	Ingredients []Ingredient
	Method      []MethodStep
	Categories  []Category
}

// Category represents a tag for a recipe.
type Category struct {
	ID   int
	Name string
}

// TagsToString joins the recipe's category names into a single, comma-separated string.
func (r *Recipe) TagsToString() string {
	if len(r.Categories) == 0 {
		return ""
	}
	names := make([]string, len(r.Categories))
	for i, cat := range r.Categories {
		names[i] = cat.Name
	}
	return strings.Join(names, ", ")
}

// RecipeInfo holds basic info about a recipe, used for listings.
type RecipeInfo struct {
	ID    int
	Title string
}

// InitDB initializes the database connection using environment variables.
func InitDB() error {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
	)

	var err error
	conn, err = sql.Open("pgx", connStr)
	if err != nil {
		return err
	}

	return conn.Ping()
}

// CloseDB closes the database connection.
func CloseDB() {
	if conn != nil {
		conn.Close()
	}
}

// DeleteRecipeByID deletes a recipe from the database.
func DeleteRecipeByID(id int) error {
	// The ON DELETE CASCADE on the foreign key constraints will handle deleting associated items.
	_, err := conn.Exec("DELETE FROM recipes WHERE id = $1", id)
	return err
}

// UpdateRecipe updates an existing recipe, its ingredients, and method steps in a transaction.
func UpdateRecipe(id int, title string, ingredients []Ingredient, method []MethodStep, tags []string) error {
	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Update the recipe title
	_, err = tx.Exec("UPDATE recipes SET title = $1 WHERE id = $2", title, id)
	if err != nil {
		return fmt.Errorf("failed to update recipe title: %w", err)
	}

	// 2. Delete old ingredients and method steps
	if _, err = tx.Exec("DELETE FROM ingredients WHERE recipe_id = $1", id); err != nil {
		return fmt.Errorf("failed to delete old ingredients: %w", err)
	}
	if _, err = tx.Exec("DELETE FROM method_steps WHERE recipe_id = $1", id); err != nil {
		return fmt.Errorf("failed to delete old method steps: %w", err)
	}
	if _, err = tx.Exec("DELETE FROM recipe_categories WHERE recipe_id = $1", id); err != nil {
		return fmt.Errorf("failed to delete old recipe categories: %w", err)
	}

	// Handle categories
	if len(tags) > 0 {
		stmtCat, err := tx.Prepare(`INSERT INTO categories (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`)
		if err != nil {
			return fmt.Errorf("failed to prepare category statement: %w", err)
		}
		defer stmtCat.Close()

		stmtLink, err := tx.Prepare("INSERT INTO recipe_categories (recipe_id, category_id) VALUES ($1, $2)")
		if err != nil {
			return fmt.Errorf("failed to prepare link statement: %w", err)
		}
		defer stmtLink.Close()

		for _, tagName := range tags {
			if tagName == "" {
				continue
			}
			var categoryID int
			if err := stmtCat.QueryRow(tagName).Scan(&categoryID); err != nil {
				return fmt.Errorf("failed to find/create category '%s': %w", tagName, err)
			}
			if _, err := stmtLink.Exec(id, categoryID); err != nil {
				return fmt.Errorf("failed to link category '%s' to recipe: %w", tagName, err)
			}
		}
	}

	// 3. Insert new ingredients
	stmtIng, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, measurement) VALUES($1, $2, $3, $4)")
	if err != nil {
		return fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmtIng.Close()

	for _, ing := range ingredients {
		if _, err := stmtIng.Exec(id, ing.Name, ing.Amount, ing.Measurement); err != nil {
			return fmt.Errorf("failed to insert ingredient %s: %w", ing.Name, err)
		}
	}

	// 4. Insert new method steps
	stmtSteps, err := tx.Prepare("INSERT INTO method_steps(recipe_id, step_number, description) VALUES($1, $2, $3)")
	if err != nil {
		return fmt.Errorf("failed to prepare method step statement: %w", err)
	}
	defer stmtSteps.Close()

	for _, step := range method {
		if _, err := stmtSteps.Exec(id, step.StepNumber, step.Description); err != nil {
			return fmt.Errorf("failed to insert method step %d: %w", step.StepNumber, err)
		}
	}

	return tx.Commit()
}

// FetchAllRecipeInfos retrieves the ID and title of all recipes.
func FetchAllRecipeInfos() ([]RecipeInfo, error) {
	rows, err := conn.Query("SELECT id, title FROM recipes ORDER BY title ASC")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all recipe infos: %w", err)
	}
	defer rows.Close()

	var recipes []RecipeInfo
	for rows.Next() {
		var r RecipeInfo
		if err := rows.Scan(&r.ID, &r.Title); err != nil {
			return nil, fmt.Errorf("failed to scan recipe info: %w", err)
		}
		recipes = append(recipes, r)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating recipe infos: %w", err)
	}

	return recipes, nil
}

// FetchRecipeByID retrieves a single recipe and its details from the database.
func FetchRecipeByID(id int) (*Recipe, error) {
	recipe := &Recipe{}
	queryRecipe := "SELECT id, title, created_at FROM recipes WHERE id = $1"
	err := conn.QueryRow(queryRecipe, id).Scan(&recipe.ID, &recipe.Title, &recipe.CreatedAt)
	if err != nil {
		return nil, err // Returns sql.ErrNoRows if not found
	}

	// Fetch ingredients
	rowsIng, err := conn.Query("SELECT name, amount, measurement FROM ingredients WHERE recipe_id = $1 ORDER BY id", id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ingredients: %w", err)
	}
	defer rowsIng.Close()
	for rowsIng.Next() {
		var ing Ingredient
		if err := rowsIng.Scan(&ing.Name, &ing.Amount, &ing.Measurement); err != nil {
			return nil, fmt.Errorf("failed to scan ingredient: %w", err)
		}
		recipe.Ingredients = append(recipe.Ingredients, ing)
	}
	if err = rowsIng.Err(); err != nil {
		return nil, fmt.Errorf("error iterating ingredients: %w", err)
	}

	// Fetch method steps
	rowsSteps, err := conn.Query("SELECT step_number, description FROM method_steps WHERE recipe_id = $1 ORDER BY step_number ASC", id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch method steps: %w", err)
	}
	defer rowsSteps.Close()
	for rowsSteps.Next() {
		var step MethodStep
		if err := rowsSteps.Scan(&step.StepNumber, &step.Description); err != nil {
			return nil, fmt.Errorf("failed to scan method step: %w", err)
		}
		recipe.Method = append(recipe.Method, step)
	}
	if err = rowsSteps.Err(); err != nil {
		return nil, fmt.Errorf("error iterating method steps: %w", err)
	}

	// Fetch categories
	rowsCat, err := conn.Query(`
		SELECT c.id, c.name FROM categories c
		JOIN recipe_categories rc ON c.id = rc.category_id
		WHERE rc.recipe_id = $1 ORDER BY c.name`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch categories: %w", err)
	}
	defer rowsCat.Close()
	for rowsCat.Next() {
		var cat Category
		if err := rowsCat.Scan(&cat.ID, &cat.Name); err != nil {
			return nil, fmt.Errorf("failed to scan category: %w", err)
		}
		recipe.Categories = append(recipe.Categories, cat)
	}

	return recipe, nil
}

// FetchRecipes retrieves a paginated and optionally filtered list of recipes.
func FetchRecipes(searchQuery string, limit int, offset int) ([]Recipe, int, error) {
	var countArgs []interface{}
	countQuery := "SELECT COUNT(*) FROM recipes"
	if searchQuery != "" {
		countQuery += " WHERE title ILIKE $1"
		countArgs = append(countArgs, "%"+searchQuery+"%")
	}

	var totalRecipes int
	if err := conn.QueryRow(countQuery, countArgs...).Scan(&totalRecipes); err != nil {
		return nil, 0, fmt.Errorf("failed to count recipes: %w", err)
	}

	if totalRecipes == 0 {
		return []Recipe{}, 0, nil
	}

	var queryArgs []interface{}
	query := `
		WITH paginated_recipes AS (
			SELECT id FROM recipes
	`
	if searchQuery != "" {
		query += " WHERE title ILIKE $1"
		queryArgs = append(queryArgs, "%"+searchQuery+"%")
	}
	query += " ORDER BY created_at DESC, id LIMIT $" + strconv.Itoa(len(queryArgs)+1) + " OFFSET $" + strconv.Itoa(len(queryArgs)+2)
	queryArgs = append(queryArgs, limit, offset)

	query += `
		)
		SELECT r.id, r.title, r.created_at,
		       c.id, c.name
		FROM recipes r
		LEFT JOIN recipe_categories rc ON r.id = rc.recipe_id
		LEFT JOIN categories c ON rc.category_id = c.id
		WHERE r.id IN (SELECT id FROM paginated_recipes)
		ORDER BY r.created_at DESC, r.id, c.id`

	rows, err := conn.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	recipeMap := make(map[int]*Recipe)
	var orderedRecipes []*Recipe
	for rows.Next() {
		var recipeID int
		var recipeTitle string
		var recipeCreatedAt time.Time
		var catName sql.NullString
		var catID sql.NullInt64

		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &catID, &catName); err != nil {
			return nil, 0, err
		}

		if _, ok := recipeMap[recipeID]; !ok {
			recipe := &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt}
			recipeMap[recipeID] = recipe
			orderedRecipes = append(orderedRecipes, recipe)
		}

		if catID.Valid {
			found := false
			for _, existingCat := range recipeMap[recipeID].Categories {
				if existingCat.ID == int(catID.Int64) {
					found = true
					break
				}
			}
			if !found {
				recipeMap[recipeID].Categories = append(recipeMap[recipeID].Categories, Category{ID: int(catID.Int64), Name: catName.String})
			}
		}
	}

	recipes := make([]Recipe, len(orderedRecipes))
	for i, r := range orderedRecipes {
		recipes[i] = *r
	}

	return recipes, totalRecipes, nil
}

// SaveRecipe saves a new recipe and its components to the database in a single transaction.
func SaveRecipe(title string, ingredients []Ingredient, method []MethodStep, tags []string) (int, error) {
	tx, err := conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var recipeID int
	err = tx.QueryRow("INSERT INTO recipes (title) VALUES ($1) RETURNING id", title).Scan(&recipeID)
	if err != nil {
		return 0, fmt.Errorf("failed to create recipe: %w", err)
	}

	// Handle categories
	if len(tags) > 0 {
		stmtCat, err := tx.Prepare(`INSERT INTO categories (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`)
		if err != nil {
			return 0, fmt.Errorf("failed to prepare category statement: %w", err)
		}
		defer stmtCat.Close()

		stmtLink, err := tx.Prepare("INSERT INTO recipe_categories (recipe_id, category_id) VALUES ($1, $2)")
		if err != nil {
			return 0, fmt.Errorf("failed to prepare link statement: %w", err)
		}
		defer stmtLink.Close()

		for _, tagName := range tags {
			if tagName == "" {
				continue
			}
			var categoryID int
			if err := stmtCat.QueryRow(tagName).Scan(&categoryID); err != nil {
				return 0, fmt.Errorf("failed to find/create category '%s': %w", tagName, err)
			}
			if _, err := stmtLink.Exec(recipeID, categoryID); err != nil {
				return 0, fmt.Errorf("failed to link category '%s' to recipe: %w", tagName, err)
			}
		}
	}

	// Insert ingredients
	stmtIng, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, measurement) VALUES($1, $2, $3, $4)")
	if err != nil {
		return 0, fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmtIng.Close()
	for _, ing := range ingredients {
		if _, err := stmtIng.Exec(recipeID, ing.Name, ing.Amount, ing.Measurement); err != nil {
			return 0, fmt.Errorf("failed to insert ingredient %s: %w", ing.Name, err)
		}
	}

	// Insert method steps
	stmtSteps, err := tx.Prepare("INSERT INTO method_steps(recipe_id, step_number, description) VALUES($1, $2, $3)")
	if err != nil {
		return 0, fmt.Errorf("failed to prepare method step statement: %w", err)
	}
	defer stmtSteps.Close()
	for _, step := range method {
		if _, err := stmtSteps.Exec(recipeID, step.StepNumber, step.Description); err != nil {
			return 0, fmt.Errorf("failed to insert method step %d: %w", step.StepNumber, err)
		}
	}

	return recipeID, tx.Commit()
}

// FetchRecipesByTag retrieves a paginated list of recipes filtered by a specific tag.
func FetchRecipesByTag(tagName string, limit int, offset int) ([]Recipe, int, error) {
	countQuery := `
		SELECT COUNT(DISTINCT r.id) FROM recipes r
		JOIN recipe_categories rc ON r.id = rc.recipe_id
		JOIN categories c ON rc.category_id = c.id
		WHERE c.name = $1`

	var totalRecipes int
	if err := conn.QueryRow(countQuery, tagName).Scan(&totalRecipes); err != nil {
		return nil, 0, fmt.Errorf("failed to count recipes for tag %s: %w", tagName, err)
	}

	if totalRecipes == 0 {
		return []Recipe{}, 0, nil
	}

	query := `
		WITH paginated_recipes AS (
			SELECT r.id FROM recipes r
			JOIN recipe_categories rc ON r.id = rc.recipe_id
			JOIN categories c ON rc.category_id = c.id
			WHERE c.name = $1
			ORDER BY r.created_at DESC, r.id
			LIMIT $2 OFFSET $3
		)
		SELECT r.id, r.title, r.created_at,
		       c.id, c.name
		FROM recipes r
		LEFT JOIN recipe_categories rc ON r.id = rc.recipe_id
		LEFT JOIN categories c ON rc.category_id = c.id
		WHERE r.id IN (SELECT id FROM paginated_recipes)
		ORDER BY r.created_at DESC, r.id, c.id`

	rows, err := conn.Query(query, tagName, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	recipeMap := make(map[int]*Recipe)
	var orderedRecipes []*Recipe
	for rows.Next() {
		var recipeID int
		var recipeTitle string
		var recipeCreatedAt time.Time
		var catName sql.NullString
		var catID sql.NullInt64

		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &catID, &catName); err != nil {
			return nil, 0, err
		}

		if _, ok := recipeMap[recipeID]; !ok {
			recipe := &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt}
			recipeMap[recipeID] = recipe
			orderedRecipes = append(orderedRecipes, recipe)
		}

		if catID.Valid {
			found := false
			for _, existingCat := range recipeMap[recipeID].Categories {
				if existingCat.ID == int(catID.Int64) {
					found = true
					break
				}
			}
			if !found {
				recipeMap[recipeID].Categories = append(recipeMap[recipeID].Categories, Category{ID: int(catID.Int64), Name: catName.String})
			}
		}
	}

	recipes := make([]Recipe, len(orderedRecipes))
	for i, r := range orderedRecipes {
		recipes[i] = *r
	}

	return recipes, totalRecipes, nil
}
