-- Initialize the database with required functions and tables for db-cache

-- Create table_log table
CREATE TABLE IF NOT EXISTS table_log
(
    table_name TEXT NOT NULL PRIMARY KEY,
    operation_time TIMESTAMP default CURRENT_TIMESTAMP
);

-- Create log_changes function
CREATE OR REPLACE FUNCTION log_changes() RETURNS TRIGGER LANGUAGE plpgsql AS
$FUNC$
BEGIN
    INSERT INTO table_log (table_name, operation_time) VALUES (tg_table_name, current_timestamp)
    ON CONFLICT (table_name) DO UPDATE SET operation_time = excluded.operation_time;
    
    RETURN NEW;
END
$FUNC$;

-- Create create_table_monitor_trigger function
CREATE OR REPLACE FUNCTION create_table_monitor_trigger(table_name TEXT) RETURNS VOID LANGUAGE plpgsql AS
$FUNC$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'monitor_changes' AND
            tgenabled = 'O' AND
            tgisinternal = 'f' AND
            tgrelid = (table_name::regclass)::oid
    ) THEN
        EXECUTE format('
            CREATE TRIGGER monitor_changes
            AFTER INSERT OR UPDATE OR DELETE ON %I
            FOR EACH ROW EXECUTE FUNCTION log_changes();
        ', table_name);
    END IF;
END
$FUNC$;