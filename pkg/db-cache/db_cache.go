package dbcache

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5/pgxpool"
	snowflakecache "github.com/transactrx/db-cache/pkg/snowflake-cache"
)

var initialized bool = false

type DbCache[T any] struct {
	mutex           sync.RWMutex
	databasePool    *pgxpool.Pool
	keyCache        map[string][]T
	monitoredTables []string
	loadSQL         string
	sqlParameters   []interface{}
	keyField        string
	staleCheckVal   *string
	logger          *log.Logger
}

// Cache is the minimal shared interface implemented by both Postgres and Snowflake caches.
type Cache[T any] interface {
	Get(string) []T
	GetAll() []T
	ForceRefresh() error
}

func (c *DbCache[T]) Get(index string) []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if val, ok := c.keyCache[index]; ok {
		return val
	}
	return nil
}

func (c *DbCache[T]) GetAll() []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	var result []T
	for _, val := range c.keyCache {
		result = append(result, val...)
	}
	return result
}

func (c *DbCache[T]) getDbStaleCheckValue() (*string, error) {

	checkQuery := generateStaleCheckSQL(c.monitoredTables)

	rows, err := c.databasePool.Query(context.Background(), checkQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		var value string
		rows.Scan(&value)
		return &value, nil
	} else {
		return nil, fmt.Errorf("stale check query returns no rows")
	}

}

func generateStaleCheckSQL(monitoredTables []string) string {
	var checkQuery string
	if len(monitoredTables) == 1 {
		checkQuery = fmt.Sprintf("select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from cache.table_log where table_name='%s' ", monitoredTables[0])
	} else {
		checkQuery = "select string_agg(ct, ', ') from ("
		for i := 0; i < len(monitoredTables); i++ {
			checkQuery = checkQuery + fmt.Sprintf("select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from cache.table_log where table_name='%s' ", monitoredTables[i])
			if i < len(monitoredTables)-1 {
				checkQuery = checkQuery + " union all "
			} else {
				checkQuery = checkQuery + ") as t"
			}
		}
	}
	return checkQuery
}

func (c *DbCache[T]) loadCache(staleCheckVal *string) error {

	if c.staleCheckVal != nil && *c.staleCheckVal == *staleCheckVal {
		c.logger.Printf("Cache is already up to date..")
		return nil
	}
	c.logger.Printf("Loading cache %s by %s\n", c.monitoredTables, c.keyField)

	var err error

	if err != nil {
		return err
	}

	var result []T

	err = pgxscan.Select(context.Background(), c.databasePool, &result, c.loadSQL, c.sqlParameters...)
	if err != nil {
		return err
	}

	newMap := make(map[string][]T)

	for _, newData := range result {

		keyValue, err := getKeyValue(newData, c.keyField)
		if err != nil {
			return err
		}

		if _, ok := newMap[keyValue]; !ok {
			newMap[keyValue] = []T{}
		}
		newMap[keyValue] = append(newMap[keyValue], newData)
	}

	c.staleCheckVal = staleCheckVal
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.keyCache = newMap
	return nil
}

func (cache *DbCache[T]) ForceRefresh() error {
	cache.staleCheckVal = nil
	staleCheckVal, err := cache.getDbStaleCheckValue()
	if err != nil {
		return err
	}
	cache.loadCache(staleCheckVal)

	return nil
}

func CreateCache[T any](logger *log.Logger, SQL string, monitoredTables []string, keyField string, cacheCheckInterval time.Duration, DB any, DB_RW any, SQLParams ...interface{}) (Cache[T], error) {

	// If not a pgx pool, assume Snowflake (*sql.DB) and delegate immediately.
	if _, ok := DB.(*pgxpool.Pool); !ok {
		sfDB, ok := DB.(*sql.DB)
		if !ok {
			return nil, fmt.Errorf("unsupported DB type: expected *pgxpool.Pool or *sql.DB")
		}
		var database, defaultSchema string
		if s, ok := DB_RW.(string); ok {
			// Parse "DATABASE.SCHEMA" format or just "SCHEMA"
			parts := strings.Split(s, ".")
			if len(parts) == 2 {
				database = parts[0]
				defaultSchema = parts[1]
			} else {
				defaultSchema = s
			}
		}
		return snowflakecache.CreateCacheWithDatabase[T](logger, SQL, monitoredTables, keyField, cacheCheckInterval, sfDB, database, defaultSchema, SQLParams...)
	}

	// Postgres path (unchanged)
	db := DB.(*pgxpool.Pool)
	var db_rw *pgxpool.Pool
	if w, ok := DB_RW.(*pgxpool.Pool); ok {
		db_rw = w
	}
	if logger == nil {
		logger = log.New(os.Stdout, "db_cache ", log.Lshortfile|log.Ltime)
	}
	cache := &DbCache[T]{
		databasePool:    db,
		monitoredTables: monitoredTables,
		loadSQL:         SQL,
		keyField:        keyField,
		sqlParameters:   SQLParams,
		logger:          logger,
	}
	createTableMonitoringTriggers(monitoredTables, db_rw, logger)
	staleCheckVal, err := cache.getDbStaleCheckValue()
	if err != nil {
		return nil, err
	}
	err = cache.loadCache(staleCheckVal)
	if err != nil {
		return nil, err
	}
	go func() {

		for now := range time.Tick(cacheCheckInterval) {
			staleCheckVal, err := cache.getDbStaleCheckValue()
			if err != nil {
				cache.logger.Printf("Error in cache monitor: %v", err)
			} else {
				cache.logger.Printf("time to reload cache: %s", now.String())
				err := cache.loadCache(staleCheckVal)
				if err != nil {
					cache.logger.Printf("error while reloading cache: %v", err)
				}
			}

		}
	}()
	return cache, nil

}

func createTableMonitoringTriggers(tables []string, db *pgxpool.Pool, logger *log.Logger) {
	for _, table := range tables {
		createTableMonitoringTrigger(table, db, logger)
	}
}

func createTableMonitoringTrigger(tableName string, DB *pgxpool.Pool, logger *log.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("warning: panic while creating table monitoring trigger for %s: %v (cache will still work but may not auto-refresh)", tableName, r)
		}
	}()

	sql := fmt.Sprintf(`select create_table_monitor_trigger('%s');`, tableName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := DB.Exec(ctx, sql)
	if err != nil {
		logger.Printf("warning: could not create table monitoring trigger for %s: %v (cache will still work but may not auto-refresh)", tableName, err)
	} else {
		logger.Printf("successfully created monitoring trigger for table: %s", tableName)
	}
}

func getKeyValue(obj any, keyField string) (string, error) {

	objValue := reflect.ValueOf(obj)
	objKind := objValue.Kind()
	objType := objValue.Type()
	if (objKind == reflect.Map) && (objType.Key().Kind() == reflect.String) {
		for _, mapKey := range objValue.MapKeys() {
			if strings.EqualFold(mapKey.String(), keyField) {
				return objValue.MapIndex(mapKey).Interface().(string), nil
			}
		}
		return "", fmt.Errorf("specified key field '%s' is not part of the query results", keyField)
	} else {
		// Find field by name (case-insensitive)
		objType := reflect.TypeOf(obj)
		var fv reflect.Value
		found := false

		for i := 0; i < objType.NumField(); i++ {
			field := objType.Field(i)
			if strings.EqualFold(field.Name, keyField) {
				fv = reflect.Indirect(objValue).Field(i)
				found = true
				break
			}
		}

		if !found {
			return "", fmt.Errorf("field %s is not found in the struct", keyField)
		}

		if !fv.IsValid() {
			return "", fmt.Errorf("field %s is not valid", keyField)
		}

		// Handle both string and *string fields
		if fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				return "", fmt.Errorf("key field %s is nil", keyField)
			}
			return fv.Elem().String(), nil
		}
		return fv.String(), nil
	}
}
