-- Create schema for cache monitoring
CREATE SCHEMA IF NOT EXISTS cache;

-- Create table_log for monitoring table changes
CREATE TABLE IF NOT EXISTS cache.table_log (
    id SERIAL PRIMARY KEY,
    table_name VARCHAR(255) NOT NULL,
    operation_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    operation_type VARCHAR(10) DEFAULT 'UPDATE'
);

-- Create test tables for cache testing
CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    key VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(255) UNIQUE NOT NULL,
    email VARCHAR(255) NOT NULL,
    role VARCHAR(50) DEFAULT 'user',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create function to log table changes
CREATE OR REPLACE FUNCTION log_table_change()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO cache.table_log (table_name, operation_time, operation_type)
    VALUES (TG_TABLE_NAME, CURRENT_TIMESTAMP, TG_OP);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- Create triggers for table monitoring
CREATE OR REPLACE FUNCTION create_table_monitor_trigger(table_name TEXT)
RETURNS VOID AS $$
DECLARE
    trigger_name TEXT;
BEGIN
    trigger_name := table_name || '_change_trigger';
    
    EXECUTE format('
        DROP TRIGGER IF EXISTS %I ON %I;
        CREATE TRIGGER %I
            AFTER INSERT OR UPDATE OR DELETE ON %I
            FOR EACH STATEMENT
            EXECUTE FUNCTION log_table_change();
    ', trigger_name, table_name, trigger_name, table_name);
    
    RAISE NOTICE 'Created trigger % for table %', trigger_name, table_name;
END;
$$ LANGUAGE plpgsql;

-- Create triggers for our test tables
SELECT create_table_monitor_trigger('api_keys');
SELECT create_table_monitor_trigger('users');
