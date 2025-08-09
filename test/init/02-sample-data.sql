-- Create sample tables and data for testing

-- Create api_keys table (based on the example in cmd/example/apiKeyModel.go)
CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    api_key VARCHAR(255) NOT NULL UNIQUE,
    user_id VARCHAR(100) NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create products table for additional testing
CREATE TABLE IF NOT EXISTS products (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(100) NOT NULL,
    price DECIMAL(10,2),
    in_stock BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Insert sample data into api_keys
INSERT INTO api_keys (api_key, user_id, is_active) VALUES
    ('key1_test123', 'user1', TRUE),
    ('key2_test456', 'user1', TRUE),
    ('key3_test789', 'user2', TRUE),
    ('key4_test000', 'user2', FALSE),
    ('key5_test111', 'user3', TRUE);

-- Insert sample data into products
INSERT INTO products (name, category, price, in_stock) VALUES
    ('Laptop Pro', 'electronics', 1299.99, TRUE),
    ('Wireless Mouse', 'electronics', 29.99, TRUE),
    ('Office Chair', 'furniture', 299.99, TRUE),
    ('Desk Lamp', 'furniture', 89.99, FALSE),
    ('Coffee Mug', 'office', 12.99, TRUE);

-- Set up monitoring triggers for our test tables
SELECT create_table_monitor_trigger('api_keys');
SELECT create_table_monitor_trigger('products');