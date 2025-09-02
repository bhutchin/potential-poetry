package db

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

// conn is the package-level database connection pool.
var conn *sql.DB

// Ingredient represents the structure of a single ingredient.
type Ingredient struct {
	Name   string
	Amount string
	Unit   string
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
	SourceURL   sql.NullString // For the original URL of an imported recipe
	Servings    int
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

// MealPlanInfo holds basic info about a saved meal plan.
type MealPlanInfo struct {
	ID   int
	Name string
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

// DuplicateRecipeByID creates a copy of an existing recipe and all its components.
func DuplicateRecipeByID(id int) (int, error) {
	tx, err := conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Fetch the original recipe details
	originalRecipe, err := FetchRecipeByID(id)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch original recipe for duplication: %w", err)
	}

	// 2. Create the new recipe with a modified title
	newTitle := originalRecipe.Title + " (Copy)"
	var newRecipeID int
	err = tx.QueryRow("INSERT INTO recipes (title, source_url, servings) VALUES ($1, $2, $3) RETURNING id", newTitle, originalRecipe.SourceURL, originalRecipe.Servings).Scan(&newRecipeID)
	if err != nil {
		return 0, fmt.Errorf("failed to create new recipe during duplication: %w", err)
	}

	// 3. Insert ingredients for the new recipe
	if len(originalRecipe.Ingredients) > 0 {
		stmtIng, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, unit) VALUES($1, $2, $3, $4)")
		if err != nil {
			return 0, fmt.Errorf("failed to prepare ingredient statement for duplication: %w", err)
		}
		defer stmtIng.Close()
		for _, ing := range originalRecipe.Ingredients {
			if _, err := stmtIng.Exec(newRecipeID, ing.Name, ing.Amount, ing.Unit); err != nil {
				return 0, fmt.Errorf("failed to insert duplicated ingredient %s: %w", ing.Name, err)
			}
		}
	}

	// 4. Insert method steps for the new recipe
	if len(originalRecipe.Method) > 0 {
		stmtSteps, err := tx.Prepare("INSERT INTO method_steps(recipe_id, step_number, description) VALUES($1, $2, $3)")
		if err != nil {
			return 0, fmt.Errorf("failed to prepare method step statement for duplication: %w", err)
		}
		defer stmtSteps.Close()
		for _, step := range originalRecipe.Method {
			if _, err := stmtSteps.Exec(newRecipeID, step.StepNumber, step.Description); err != nil {
				return 0, fmt.Errorf("failed to insert duplicated method step %d: %w", step.StepNumber, err)
			}
		}
	}

	// 5. Link categories to the new recipe
	if len(originalRecipe.Categories) > 0 {
		stmtLink, err := tx.Prepare("INSERT INTO recipe_categories (recipe_id, category_id) VALUES ($1, $2)")
		if err != nil {
			return 0, fmt.Errorf("failed to prepare link statement for duplication: %w", err)
		}
		defer stmtLink.Close()
		for _, cat := range originalRecipe.Categories {
			if _, err := stmtLink.Exec(newRecipeID, cat.ID); err != nil {
				return 0, fmt.Errorf("failed to link category '%s' to duplicated recipe: %w", cat.Name, err)
			}
		}
	}

	// 6. Commit the transaction
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit duplication transaction: %w", err)
	}

	return newRecipeID, nil
}

// UpdateRecipe updates an existing recipe, its ingredients, and method steps in a transaction.
func UpdateRecipe(id int, title string, sourceURL string, servings int, ingredients []Ingredient, method []MethodStep, tags []string) error {
	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Update the recipe title and source URL
	var sourceURLValue sql.NullString
	if sourceURL != "" {
		sourceURLValue.String = sourceURL
		sourceURLValue.Valid = true
	}
	_, err = tx.Exec("UPDATE recipes SET title = $1, source_url = $2, servings = $3 WHERE id = $4", title, sourceURLValue, servings, id)
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
	stmtIng, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, unit) VALUES($1, $2, $3, $4)")
	if err != nil {
		return fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmtIng.Close()

	for _, ing := range ingredients {
		if _, err := stmtIng.Exec(id, ing.Name, ing.Amount, ing.Unit); err != nil {
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
	queryRecipe := "SELECT id, title, source_url, servings, created_at FROM recipes WHERE id = $1"
	err := conn.QueryRow(queryRecipe, id).Scan(&recipe.ID, &recipe.Title, &recipe.SourceURL, &recipe.Servings, &recipe.CreatedAt)
	if err != nil {
		return nil, err // Returns sql.ErrNoRows if not found
	}

	// Fetch ingredients
	rowsIng, err := conn.Query("SELECT name, amount, unit FROM ingredients WHERE recipe_id = $1 ORDER BY id", id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ingredients: %w", err)
	}
	defer rowsIng.Close()
	for rowsIng.Next() {
		var ing Ingredient
		if err := rowsIng.Scan(&ing.Name, &ing.Amount, &ing.Unit); err != nil {
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
		SELECT r.id, r.title, r.created_at, r.source_url,
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
		var sourceURL sql.NullString
		var catName sql.NullString
		var catID sql.NullInt64

		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &sourceURL, &catID, &catName); err != nil {
			return nil, 0, err
		}

		if _, ok := recipeMap[recipeID]; !ok {
			recipe := &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt, SourceURL: sourceURL}
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

// CountSavedMealPlans returns the total number of saved meal plans.
func CountSavedMealPlans() (int, error) {
	var count int
	err := conn.QueryRow("SELECT COUNT(*) FROM meal_plans").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count saved meal plans: %w", err)
	}
	return count, nil
}

// ListSavedMealPlans retrieves a paginated list of saved meal plans from the database.
func ListSavedMealPlans(sortOrder string, limit, offset int) ([]MealPlanInfo, error) {
	orderByClause := "ORDER BY name ASC" // Default sort
	switch sortOrder {
	case "name_asc":
		orderByClause = "ORDER BY name ASC"
	case "date_desc":
		orderByClause = "ORDER BY created_at DESC"
	case "date_asc":
		orderByClause = "ORDER BY created_at ASC"
		// The default case is already handled, but this switch makes the logic clear
		// and prevents any other values from being used in the query.
	}

	query := "SELECT id, name FROM meal_plans " + orderByClause + " LIMIT $1 OFFSET $2"
	rows, err := conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch saved meal plans: %w", err)
	}
	defer rows.Close()

	var plans []MealPlanInfo
	for rows.Next() {
		var p MealPlanInfo
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, fmt.Errorf("failed to scan saved meal plan: %w", err)
		}
		plans = append(plans, p)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating saved meal plans: %w", err)
	}

	return plans, nil
}

// SaveNamedMealPlan saves or updates a meal plan's JSON data with a given name.
func SaveNamedMealPlan(name string, data string) error {
	// Using ON CONFLICT to allow users to update a plan by saving with the same name.
	query := `
		INSERT INTO meal_plans (name, data) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data`
	_, err := conn.Exec(query, name, data)
	if err != nil {
		return fmt.Errorf("failed to save named meal plan '%s': %w", name, err)
	}
	return nil
}

// LoadNamedMealPlan retrieves the JSON data of a saved meal plan by its ID.
func LoadNamedMealPlan(id int) (string, error) {
	var data string
	query := "SELECT data FROM meal_plans WHERE id = $1"
	err := conn.QueryRow(query, id).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no meal plan found with id %d", id)
		}
		return "", fmt.Errorf("failed to load meal plan data: %w", err)
	}
	return data, nil
}

// DeleteNamedMealPlan deletes a saved meal plan by its ID.
func DeleteNamedMealPlan(id int) error {
	_, err := conn.Exec("DELETE FROM meal_plans WHERE id = $1", id)
	return err
}

// SaveRecipe saves a new recipe and its components to the database in a single transaction.
func SaveRecipe(title string, sourceURL string, servings int, ingredients []Ingredient, method []MethodStep, tags []string) (int, error) {
	tx, err := conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var sourceURLValue sql.NullString
	if sourceURL != "" {
		sourceURLValue.String = sourceURL
		sourceURLValue.Valid = true
	}

	var recipeID int
	err = tx.QueryRow("INSERT INTO recipes (title, source_url, servings) VALUES ($1, $2, $3) RETURNING id", title, sourceURLValue, servings).Scan(&recipeID)
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
	stmtIng, err := tx.Prepare("INSERT INTO ingredients(recipe_id, name, amount, unit) VALUES($1, $2, $3, $4)")
	if err != nil {
		return 0, fmt.Errorf("failed to prepare ingredient statement: %w", err)
	}
	defer stmtIng.Close()
	for _, ing := range ingredients {
		if _, err := stmtIng.Exec(recipeID, ing.Name, ing.Amount, ing.Unit); err != nil {
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
		SELECT r.id, r.title, r.created_at, r.source_url,
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
		var sourceURL sql.NullString
		var catName sql.NullString
		var catID sql.NullInt64

		if err := rows.Scan(&recipeID, &recipeTitle, &recipeCreatedAt, &sourceURL, &catID, &catName); err != nil {
			return nil, 0, err
		}

		if _, ok := recipeMap[recipeID]; !ok {
			recipe := &Recipe{ID: recipeID, Title: recipeTitle, CreatedAt: recipeCreatedAt, SourceURL: sourceURL}
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
