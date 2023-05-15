The DB CACHE requires that the following SQL resources

***NOTE:*** The following SQL scripts are provided as a convenience.  They are not required to be used.  The only requirement is that the SQL resources exist in the database.
```sql
CREATE  TABLE IF NOT EXISTS table_log (
   table_name text PRIMARY KEY,
   operation_time timestamp default current_timestamp
);
alter table table_log owner to rds_superuser;

CREATE OR REPLACE FUNCTION log_changes()
RETURNS TRIGGER AS $$
BEGIN
   INSERT INTO table_log(table_name, operation_time)
   VALUES (TG_TABLE_NAME, current_timestamp)
   ON CONFLICT (table_name)
   DO UPDATE SET operation_time = excluded.operation_time;

   RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION create_table_monitor_trigger(table_name text)
RETURNS VOID AS $$
BEGIN
   IF NOT EXISTS (
      SELECT 1
      FROM pg_trigger
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
END;
$$ LANGUAGE plpgsql;;
```