# REST API to MySQL Healthchecks

Go REST API that checks a various parameters in a MySQL database and returns JSON and HTTP codes about the parameter checked on database. By default, it runs on port 3307 and takes a file stored at $HOME/.my.cnf by parsing it to get parameters to build DSN to create a DB connection.

## Run 
```sh
go run mysql_health_check.go [-port <port_number>]
```

## Usage
To check if a database is in readonly mode, you can execute the following command:
```sh
curl -i localhost:3307/status/rw
```
