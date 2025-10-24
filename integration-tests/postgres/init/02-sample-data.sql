-- Insert sample data for testing
INSERT INTO api_keys (key, name, is_active) VALUES 
    ('api_key_1', 'Test API Key 1', true),
    ('api_key_2', 'Test API Key 2', true),
    ('api_key_3', 'Test API Key 3', false),
    ('api_key_4', 'Test API Key 4', true)
ON CONFLICT (key) DO NOTHING;

INSERT INTO users (username, email, role) VALUES 
    ('alice', 'alice@example.com', 'admin'),
    ('bob', 'bob@example.com', 'user'),
    ('charlie', 'charlie@example.com', 'user'),
    ('diana', 'diana@example.com', 'moderator')
ON CONFLICT (username) DO NOTHING;

-- Insert initial log entries to establish baseline
INSERT INTO table_log (table_name, operation_time, operation_type) VALUES 
    ('api_keys', CURRENT_TIMESTAMP, 'INSERT'),
    ('users', CURRENT_TIMESTAMP, 'INSERT');
