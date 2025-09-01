-- This script sets up the database schema for the Potential Poetry recipe application.

-- Enable the pg_trgm extension for faster text searching (used for recipe titles and tags).
-- You may need to run this as a database superuser if you get a permission error.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- This table stores the main information about each recipe.
CREATE TABLE recipes (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- This table stores the ingredients for each recipe.
-- The `recipe_id` is a foreign key that links to the `recipes` table.
-- `ON DELETE CASCADE` ensures that if a recipe is deleted, its ingredients are also deleted.
CREATE TABLE ingredients (
    id SERIAL PRIMARY KEY,
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    amount TEXT,
    unit TEXT
);

-- This table stores the steps for the recipe's method.
CREATE TABLE method_steps (
    id SERIAL PRIMARY KEY,
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    step_number INTEGER NOT NULL,
    description TEXT NOT NULL
);

-- This table stores the tags/categories for recipes.
-- The name of each category must be unique.
CREATE TABLE categories (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
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
    name TEXT NOT NULL UNIQUE,
    data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
