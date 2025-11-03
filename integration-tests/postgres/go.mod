module github.com/transactrx/db-cache/integration-tests/postgres

go 1.24

toolchain go1.24.5

require (
	github.com/jackc/pgx/v5 v5.7.5
	github.com/stretchr/testify v1.10.0
	github.com/transactrx/db-cache v0.0.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/georgysavva/scany/v2 v2.1.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/transactrx/db-cache => ../../
