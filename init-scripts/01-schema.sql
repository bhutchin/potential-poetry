-- This script sets up the database schema for the Potential Poetry recipe application.

-- This table stores the main information about each recipe.
CREATE TABLE recipes (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    source_url TEXT,
    servings INTEGER DEFAULT 4,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- This table stores the ingredients for each recipe.
-- The `recipe_id` is a foreign key that links to the `recipes` table.
-- `ON DELETE CASCADE` ensures that if a recipe is deleted, its ingredients are also deleted.
CREATE TABLE ingredients (
    id SERIAL PRIMARY KEY,
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    amount TEXT NOT NULL,
    unit TEXT NOT NULL
);

-- This table stores the steps for the recipe's method.
CREATE TABLE method_steps (
    id SERIAL PRIMARY KEY,
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    step_number INTEGER NOT NULL,
    description TEXT NOT NULL,
    UNIQUE(recipe_id, step_number)
);

-- This table stores the tags/categories for recipes.
-- The name of each category must be unique.
CREATE TABLE categories (
    id SERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL
);

-- This is a join table to create a many-to-many relationship
-- between recipes and categories.
CREATE TABLE recipe_categories (
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    PRIMARY KEY (recipe_id, category_id)
);

-- This table stores saved meal plans.
CREATE TABLE meal_plans (
    id SERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- This table stores user-defined overrides for ingredient categories.
CREATE TABLE ingredient_category_overrides (
    ingredient_name TEXT PRIMARY KEY, -- Stored as lowercase
    category_name TEXT NOT NULL
);

-- This table stores the grocery store categories for the shopping list.
CREATE TABLE grocery_categories (
    id SERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    display_order INTEGER DEFAULT 0
);

-- This table stores keywords used to automatically assign ingredients to a grocery category.
CREATE TABLE grocery_category_keywords (
    id SERIAL PRIMARY KEY,
    category_id INTEGER NOT NULL REFERENCES grocery_categories(id) ON DELETE CASCADE,
    keyword TEXT NOT NULL
);