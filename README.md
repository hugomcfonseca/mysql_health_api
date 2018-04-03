# mysql_health_api

`mysql_health_api` is a REST API, developed in Go, used to perform multiple health-checks in MySQL databases. Database's health-checks are performed by getting a given endpoint of REST API, which returns an HTTP code and JSON content according a health-check, or not. For instance, if a health-check is returned with success, it will return `200 OK` as HTTP code, and a JSON object.

## Getting Started

By default, it listens on port 3307 and reads from a file stored at $HOME/.my.cnf to parse it and to get parameters to build DSN to create a DB connection.

```sh
go run mysql_health_check.go [-port <port_number>]
```

## Usage

To check if a database is in readonly mode, you can execute the following command:

```sh
curl -i localhost:3307/status/rw
```
