package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	dbcache "github.com/transactrx/db-cache/pkg/db-cache"
	"log"
	"time"
)

var pool *pgxpool.Pool
var keyCache dbcache.DbCache[ApiKey2]

func main() {
	
	
	initializeDBPoolOrPanic("postgres://user:password@host:5432/db")

	keyCache, err := dbcache.CreateCache[ApiKey2](nil, "select key, description, configuration, name, max_daily_rate, volumes,client_id as clientid from api_keys", []string{"api_keys"}, "Key", time.Second*43, pool)
	if err != nil {
		panic(err)
	}

	result := keyCache.Get("someid")

	if result != nil {
		log.Printf("%v", result)
	} else {
		log.Printf("Value not found in cache!")
	}

	
	time.Sleep(time.Minute * 20)

}

func initializeDBPoolOrPanic(url string) {
	var err error
	pool, err = pgxpool.New(context.Background(), url)
	if err != nil {
		log.Panicf("Could not create database pool: %v", err)
	}
}
