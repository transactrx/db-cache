-- Insert sample data for Snowflake testing
INSERT INTO API_KEYS (KEY, NAME, IS_ACTIVE) VALUES 
    ('api_key_1', 'Test API Key 1', TRUE),
    ('api_key_2', 'Test API Key 2', TRUE),
    ('api_key_3', 'Test API Key 3', FALSE),
    ('api_key_4', 'Test API Key 4', TRUE);

INSERT INTO USERS (USERNAME, EMAIL, ROLE) VALUES 
    ('alice', 'alice@example.com', 'admin'),
    ('bob', 'bob@example.com', 'user'),
    ('charlie', 'charlie@example.com', 'user'),
    ('diana', 'diana@example.com', 'moderator');

-- Insert initial log entries to establish baseline
INSERT INTO CACHE.TABLE_LOG (TABLE_NAME, OPERATION_TIME, OPERATION_TYPE) VALUES 
    ('API_KEYS', CURRENT_TIMESTAMP(), 'INSERT'),
    ('USERS', CURRENT_TIMESTAMP(), 'INSERT');

