package dbcache

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sqlCreateTableLogTable = `
CREATE TABLE IF NOT EXISTS table_log
(
    table_name TEXT NOT NULL PRIMARY KEY,
    operation_time TIMESTAMP default CURRENT_TIMESTAMP
)
`

const sqlCreateLogChangesFunction = `
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM information_schema.routines WHERE routine_name = 'log_changes')
	THEN
	    CREATE FUNCTION log_changes() RETURNS TRIGGER LANGUAGE plpgsql AS
		$FUNC$
			BEGIN
				INSERT INTO table_log (table_name, operation_time) VALUES (tg_table_name, current_timestamp)
				ON CONFLICT (table_name) DO UPDATE SET operation_time = excluded.operation_time;
			
				RETURN NEW;
			END
		$FUNC$;
	END IF;
END
$$
`

const sqlCreateTableMonitorTrigger = `
DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM information_schema.routines WHERE routine_name = 'create_table_monitor_trigger') 
	THEN 
    	CREATE FUNCTION create_table_monitor_trigger(table_name TEXT) RETURNS VOID LANGUAGE plpgsql AS
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
	END IF;
END
$$
`

func CreateDbTriggersAndTables(db *pgxpool.Pool) error {

	_, err := db.Exec(context.Background(), sqlCreateTableLogTable)
	if err != nil {
		return fmt.Errorf("failed to create the tablelog table: %w", err)
	}

	_, err = db.Exec(context.Background(), sqlCreateLogChangesFunction)
	if err != nil {
		return fmt.Errorf("failed to create the logchanges function: %w", err)
	}

	_, err = db.Exec(context.Background(), sqlCreateTableMonitorTrigger)
	if err != nil {
		return fmt.Errorf("failed to create the cache tables monitoring trigger: %w", err)
	}

	return nil
}
