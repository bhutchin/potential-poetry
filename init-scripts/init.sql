-- This script will be executed when the PostgreSQL container is first created.

CREATE TABLE IF NOT EXISTS recipes (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ingredients (
    id SERIAL PRIMARY KEY,
    recipe_id INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    amount VARCHAR(50) NOT NULL,
    measurement VARCHAR(50) NOT NULL
);