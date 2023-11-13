package dbcache

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5/pgxpool"
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
		checkQuery = fmt.Sprintf("select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from table_log where table_name='%s' ", monitoredTables[0])
	} else {
		checkQuery = "select string_agg(ct, ', ') from ("
		for i := 0; i < len(monitoredTables); i++ {
			checkQuery = checkQuery + fmt.Sprintf("select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from table_log where table_name='%s' ", monitoredTables[i])
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

func CreateCache[T any](logger *log.Logger, SQL string, monitoredTables []string, keyField string, cacheCheckInterval time.Duration, DB *pgxpool.Pool, DB_RW *pgxpool.Pool, SQLParams ...interface{}) (*DbCache[T], error) {

	if logger == nil {
		logger = log.New(os.Stdout, "db_cache ", log.Lshortfile|log.Ltime)
	}
	cache := &DbCache[T]{
		databasePool:    DB,
		monitoredTables: monitoredTables,
		loadSQL:         SQL,
		keyField:        keyField,
		sqlParameters:   SQLParams,
		logger:          logger,
	}
	err := createTableMonitoringTriggers(monitoredTables, DB_RW)
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

func createTableMonitoringTriggers(tables []string, db *pgxpool.Pool) error {
	for _, table := range tables {
		err := createTableMonitoringTrigger(table, db)
		if err != nil {
			return err
		}
	}
	return nil
}

func createTableMonitoringTrigger(tableName string, DB *pgxpool.Pool) error {
	sql := fmt.Sprintf(`select create_table_monitor_trigger('%s');`, tableName)
	_, err := DB.Exec(context.Background(), sql)
	if err != nil {
		log.Printf("error while creating table monitoring trigger for %s: %v", tableName, err)
	}
	return err

}

func getKeyValue(obj any, keyField string) (string, error) {
	r := reflect.ValueOf(obj)
	fv := reflect.Indirect(r).FieldByName(keyField)
	if !fv.IsValid() {
		return "", fmt.Errorf("field %s is not found in the APIKey struct", keyField)
	}
	return fv.Elem().String(), nil

}
