-- This script populates the database with some sample recipes.
-- It will run after 01-schema.sql when the database is first created.

-- Sample Recipe 1: Classic Chocolate Chip Cookies
WITH recipe1 AS (
    INSERT INTO recipes (title, source_url, servings)
    VALUES ('Classic Chocolate Chip Cookies', 'https://www.example.com/cookies', 24)
    RETURNING id
)
INSERT INTO ingredients (recipe_id, name, amount, unit)
SELECT id, name, amount, unit FROM recipe1, (VALUES
    ('All-Purpose Flour', '2.25', 'cups'),
    ('Baking Soda', '1', 'tsp'),
    ('Salt', '1', 'tsp'),
    ('Unsalted Butter, softened', '1', 'cup'),
    ('Granulated Sugar', '0.75', 'cup'),
    ('Packed Brown Sugar', '0.75', 'cup'),
    ('Vanilla Extract', '1', 'tsp'),
    ('Large Eggs', '2', ''),
    ('Semi-Sweet Chocolate Chips', '2', 'cups')
) AS ing(name, amount, unit);

WITH recipe1 AS (
    SELECT id FROM recipes WHERE title = 'Classic Chocolate Chip Cookies'
)
INSERT INTO method_steps (recipe_id, step_number, description)
SELECT id, step_number, description FROM recipe1, (VALUES
    (1, 'Preheat oven to 375°F (190°C).'),
    (2, 'In a small bowl, whisk together flour, baking soda, and salt.'),
    (3, 'In a large bowl, beat butter, granulated sugar, brown sugar, and vanilla extract until creamy. Add eggs, one at a time, beating well after each addition.'),
    (4, 'Gradually beat in flour mixture. Stir in chocolate chips.'),
    (5, 'Drop by rounded tablespoon onto ungreased baking sheets.'),
    (6, 'Bake for 9 to 11 minutes or until golden brown. Cool on baking sheets for 2 minutes; remove to wire racks to cool completely.')
) AS steps(step_number, description);

WITH cat_dessert AS (INSERT INTO categories (name) VALUES ('dessert') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     cat_baking AS (INSERT INTO categories (name) VALUES ('baking') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     cat_classic AS (INSERT INTO categories (name) VALUES ('classic') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     recipe1 AS (SELECT id FROM recipes WHERE title = 'Classic Chocolate Chip Cookies')
INSERT INTO recipe_categories (recipe_id, category_id)
SELECT r1.id, cid.id FROM recipe1 r1, (SELECT id FROM cat_dessert UNION ALL SELECT id FROM cat_baking UNION ALL SELECT id FROM cat_classic) AS cid;


-- Sample Recipe 2: Simple Tomato Pasta
WITH recipe2 AS (
    INSERT INTO recipes (title, source_url, servings)
    VALUES ('Simple Tomato Pasta', 'https://www.example.com/pasta', 4)
    RETURNING id
)
INSERT INTO ingredients (recipe_id, name, amount, unit)
SELECT id, name, amount, unit FROM recipe2, (VALUES
    ('Spaghetti', '1', 'pound'),
    ('Olive Oil', '2', 'tbsp'),
    ('Garlic, minced', '4', 'cloves'),
    ('Crushed Tomatoes', '28', 'ounce'),
    ('Dried Oregano', '1', 'tsp'),
    ('Salt and Pepper', 'to taste', ''),
    ('Fresh Basil, chopped', '0.25', 'cup')
) AS ing(name, amount, unit);

WITH recipe2 AS (SELECT id FROM recipes WHERE title = 'Simple Tomato Pasta')
INSERT INTO method_steps (recipe_id, step_number, description)
SELECT id, step_number, description FROM recipe2, (VALUES
    (1, 'Cook spaghetti according to package directions. Drain and set aside.'),
    (2, 'In a large skillet, heat olive oil over medium heat. Add garlic and cook until fragrant, about 1 minute.'),
    (3, 'Stir in crushed tomatoes and oregano. Season with salt and pepper. Bring to a simmer and cook for 10 minutes.'),
    (4, 'Stir in the cooked spaghetti and fresh basil. Serve immediately.')
) AS steps(step_number, description);

WITH cat_dinner AS (INSERT INTO categories (name) VALUES ('dinner') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     cat_pasta AS (INSERT INTO categories (name) VALUES ('pasta') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     cat_quick AS (INSERT INTO categories (name) VALUES ('quick') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     cat_vegetarian AS (INSERT INTO categories (name) VALUES ('vegetarian') ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id),
     recipe2 AS (SELECT id FROM recipes WHERE title = 'Simple Tomato Pasta')
INSERT INTO recipe_categories (recipe_id, category_id)
SELECT r2.id, cid.id FROM recipe2 r2, (SELECT id FROM cat_dinner UNION ALL SELECT id FROM cat_pasta UNION ALL SELECT id FROM cat_quick UNION ALL SELECT id FROM cat_vegetarian) AS cid;


-- Populate default grocery categories and keywords
WITH cat_produce AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Produce', 1) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_produce, unnest(ARRAY['lettuce', 'onion', 'garlic', 'tomato', 'potato', 'apple', 'banana', 'orange', 'lemon', 'lime', 'pepper', 'carrot', 'broccoli', 'spinach', 'celery', 'cucumber', 'avocado', 'basil', 'parsley', 'cilantro', 'rosemary', 'thyme', 'ginger']) AS k(k);

WITH cat_meat AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Meat & Seafood', 2) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_meat, unnest(ARRAY['chicken', 'beef', 'pork', 'lamb', 'turkey', 'sausage', 'bacon', 'ham', 'steak', 'mince', 'salmon', 'tuna', 'shrimp', 'cod', 'fish']) AS k(k);

WITH cat_dairy AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Dairy & Eggs', 3) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_dairy, unnest(ARRAY['milk', 'cheese', 'cheddar', 'mozzarella', 'parmesan', 'yogurt', 'butter', 'cream', 'sour cream', 'egg']) AS k(k);

WITH cat_bakery AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Bakery', 4) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_bakery, unnest(ARRAY['bread', 'baguette', 'buns', 'rolls', 'bagel', 'croissant', 'tortilla']) AS k(k);

WITH cat_pantry AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Pantry', 5) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_pantry, unnest(ARRAY['flour', 'sugar', 'salt', 'oil', 'vinegar', 'pasta', 'rice', 'beans', 'lentils', 'canned', 'crushed tomatoes', 'broth', 'stock', 'soy sauce', 'ketchup', 'mustard', 'mayonnaise', 'spices', 'oregano', 'paprika', 'cumin', 'cinnamon', 'nutmeg', 'vanilla extract', 'baking soda', 'baking powder', 'yeast', 'chocolate chips', 'nuts', 'almonds', 'walnuts', 'peanuts', 'honey', 'syrup', 'oats']) AS k(k);

WITH cat_frozen AS (INSERT INTO grocery_categories (name, display_order) VALUES ('Frozen', 6) RETURNING id)
INSERT INTO grocery_category_keywords (category_id, keyword)
SELECT id, k FROM cat_frozen, unnest(ARRAY['frozen peas', 'frozen corn', 'ice cream']) AS k(k);

INSERT INTO grocery_categories (name, display_order) VALUES ('Other', 99);