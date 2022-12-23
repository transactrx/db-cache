package dbcache

import (
	"context"
	"fmt"
	"github.com/georgysavva/scany/v2/pgxscan"

	"github.com/jackc/pgx/v5/pgxpool"

	"reflect"
	"sync"
	"time"
)

type DbCache[T any] struct {
	mutex           sync.RWMutex
	databasePool    *pgxpool.Pool
	keyCache        map[string][]T
	monitoredTables []string
	loadSQL         string
	sqlParameters   []interface{}
	keyField        string
	staleCheckVal   *string
}

func (c *DbCache[T]) Get(index string) (interface{}, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if val, ok := c.keyCache[index]; ok {
		return val, nil
	}
	return nil, fmt.Errorf("sd")
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
		checkQuery = fmt.Sprintf("select cast(count(*) as varchar) || cast( case when max(pg_xact_commit_timestamp(xmin)) is null then '1970-01-01 00:00:01.0000+00' else max(pg_xact_commit_timestamp(xmin)) end as varchar) as ct from %s", monitoredTables[0])
	} else {
		checkQuery = "select string_agg(ct, ', ') from ("
		for i := 0; i < len(monitoredTables); i++ {
			checkQuery = checkQuery + fmt.Sprintf("select cast(count(*) as varchar) || cast( case when max(pg_xact_commit_timestamp(xmin)) is null then '1970-01-01 00:00:01.0000+00' else max(pg_xact_commit_timestamp(xmin)) end as varchar) as ct from %s", monitoredTables[i])
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
		fmt.Printf("Cache is already up to date..")
		return nil
	}
	fmt.Printf("Loading cache %s by %s\n", c.monitoredTables, c.keyField)

	var err error

	if err != nil {
		return err
	}

	var result []*T

	err = pgxscan.Select(context.Background(), c.databasePool, &result, c.loadSQL, c.sqlParameters...)
	if err != nil {
		fmt.Printf("Err:%v", err)
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
		newMap[keyValue] = append(newMap[keyValue], *newData)
	}

	c.staleCheckVal = staleCheckVal
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.keyCache = newMap
	return nil
}

func CreateCache[T any](SQL string, monitoredTables []string, keyField string, cacheCheckInterval time.Duration, DB *pgxpool.Pool, SQLParams ...interface{}) (*DbCache[T], error) {

	cache := &DbCache[T]{
		databasePool:    DB,
		monitoredTables: monitoredTables,
		loadSQL:         SQL,
		keyField:        keyField,
		sqlParameters:   SQLParams,
	}

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
				fmt.Printf("Error in cache monitor: %v", err)
			} else {
				fmt.Println(now, cache.loadCache(staleCheckVal))
			}

		}
	}()
	return cache, nil

}

func getKeyValue(obj any, keyField string) (string, error) {
	r := reflect.ValueOf(obj)
	fv := reflect.Indirect(r).FieldByName(keyField)
	if !fv.IsValid() {
		return "", fmt.Errorf("field %s is not found in the APIKey struct", keyField)
	}
	return fv.Elem().String(), nil

}
