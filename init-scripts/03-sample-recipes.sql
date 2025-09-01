-- This script inserts sample recipes into the database if they don't already exist.
DO $$
DECLARE
    recipe_id_1 INT;
    recipe_id_2 INT;
    category_id_dessert INT;
    category_id_baking INT;
    category_id_dinner INT;
    category_id_quick INT;
BEGIN
    -- Check if sample data already exists to prevent duplicate entries on re-init
    IF EXISTS (SELECT 1 FROM recipes WHERE title = 'Classic Chocolate Chip Cookies') THEN
        RAISE NOTICE 'Sample recipes already exist. Skipping insertion.';
        RETURN;
    END IF;

    RAISE NOTICE 'Inserting sample recipes...';

    -- Insert categories and get their IDs
    INSERT INTO categories (name) VALUES ('dessert') ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id INTO category_id_dessert;
    INSERT INTO categories (name) VALUES ('baking') ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id INTO category_id_baking;
    INSERT INTO categories (name) VALUES ('dinner') ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id INTO category_id_dinner;
    INSERT INTO categories (name) VALUES ('quick') ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id INTO category_id_quick;

    -- Recipe 1: Chocolate Chip Cookies
    INSERT INTO recipes (title) VALUES ('Classic Chocolate Chip Cookies') RETURNING id INTO recipe_id_1;

    -- Ingredients for Recipe 1
    INSERT INTO ingredients (recipe_id, name, amount, measurement) VALUES
        (recipe_id_1, 'all-purpose flour', '2.25', 'cups'),
        (recipe_id_1, 'baking soda', '1', 'teaspoon'),
        (recipe_id_1, 'salt', '0.5', 'teaspoon'),
        (recipe_id_1, 'unsalted butter, softened', '1', 'cup'),
        (recipe_id_1, 'granulated sugar', '0.75', 'cup'),
        (recipe_id_1, 'packed brown sugar', '0.75', 'cup'),
        (recipe_id_1, 'vanilla extract', '1', 'teaspoon'),
        (recipe_id_1, 'large eggs', '2', ''),
        (recipe_id_1, 'semi-sweet chocolate chips', '2', 'cups');

    -- Method for Recipe 1
    INSERT INTO method_steps (recipe_id, step_number, description) VALUES
        (recipe_id_1, 1, 'Preheat oven to 375°F (190°C).'),
        (recipe_id_1, 2, 'Combine flour, baking soda, and salt in a small bowl.'),
        (recipe_id_1, 3, 'Beat butter, granulated sugar, brown sugar, and vanilla extract in a large mixer bowl until creamy.'),
        (recipe_id_1, 4, 'Add eggs, one at a time, beating well after each addition.'),
        (recipe_id_1, 5, 'Gradually beat in flour mixture.'),
        (recipe_id_1, 6, 'Stir in chocolate chips.'),
        (recipe_id_1, 7, 'Drop by rounded tablespoon onto ungreased baking sheets.'),
        (recipe_id_1, 8, 'Bake for 9 to 11 minutes or until golden brown. Cool on baking sheets for 2 minutes; remove to wire racks to cool completely.');

    -- Link categories for Recipe 1
    INSERT INTO recipe_categories (recipe_id, category_id) VALUES (recipe_id_1, category_id_dessert), (recipe_id_1, category_id_baking);

    -- Recipe 2: Simple Tomato Pasta
    INSERT INTO recipes (title) VALUES ('Quick Tomato Pasta') RETURNING id INTO recipe_id_2;

    -- Ingredients for Recipe 2
    INSERT INTO ingredients (recipe_id, name, amount, measurement) VALUES
        (recipe_id_2, 'pasta (like spaghetti or penne)', '1', 'pound'), (recipe_id_2, 'olive oil', '2', 'tablespoons'), (recipe_id_2, 'garlic, minced', '3', 'cloves'), (recipe_id_2, 'canned crushed tomatoes', '28', 'ounces'), (recipe_id_2, 'dried oregano', '1', 'teaspoon'), (recipe_id_2, 'salt', '0.5', 'teaspoon'), (recipe_id_2, 'black pepper', '0.25', 'teaspoon'), (recipe_id_2, 'fresh basil, chopped', '0.25', 'cup');

    -- Method for Recipe 2
    INSERT INTO method_steps (recipe_id, step_number, description) VALUES
        (recipe_id_2, 1, 'Cook pasta according to package directions. Drain and set aside.'), (recipe_id_2, 2, 'While pasta is cooking, heat olive oil in a large skillet over medium heat.'), (recipe_id_2, 3, 'Add garlic and cook until fragrant, about 1 minute.'), (recipe_id_2, 4, 'Stir in crushed tomatoes, oregano, salt, and pepper. Bring to a simmer and cook for 10 minutes, stirring occasionally.'), (recipe_id_2, 5, 'Stir in the cooked pasta and fresh basil. Serve immediately.');

    -- Link categories for Recipe 2
    INSERT INTO recipe_categories (recipe_id, category_id) VALUES (recipe_id_2, category_id_dinner), (recipe_id_2, category_id_quick);

END $$;