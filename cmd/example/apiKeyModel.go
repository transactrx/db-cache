package main

type ApiKey2 struct {
	Key           *string
	Name          *string
	Description   *string
	Clientid      *string
	Configuration *string
	Volumes       *string
	MaxDailyRate  *int64
}
type ApiKey struct {
	Key           *string `db:"key" json:"key"`
	Name          *string `db:"name" json:"name"`
	Description   *string `db:"description" json:"description"`
	ClientID      *string `db:"client_id" json:"client_id"`
	Configuration *string `db:"configuration" json:"configuration"`
	Volumes       *string `db:"volumes" json:"volumes"`
	MaxDailyRate  *int64  `db:"max_daily_rate" json:"maxDailyRate"`
}
