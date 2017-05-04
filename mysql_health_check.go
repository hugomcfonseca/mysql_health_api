package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ini/ini"
	_ "github.com/go-sql-driver/mysql"
)

// HTTPResponse Structure used to define response object of every route request
type HTTPResponse struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
}

// ResponseType Constant
const ResponseType = "application/json"

// ContentType Constant
const ContentType = "Content-Type"

var db *sql.DB
var lag int

func main() {

	cfg, err := ini.Load(os.Getenv("HOME") + "/.my.cnf") // change me

	if err != nil {
		log.Panic(err)
	}

	dbUser := cfg.Section("client").Key("user").String()
	dbPass := cfg.Section("client").Key("password").String()
	dbHost := cfg.Section("client").Key("hostname").String()

	db, err = sql.Open("mysql", dbUser+":"+dbPass+"@"+dbHost+"/mysql")

	if err := db.Ping(); err != nil {
		log.Panic(err)
	}

	defer db.Close()

	router := http.NewServeMux()

	router.HandleFunc("/status/ro", RouteStatusReadOnly)
	router.HandleFunc("/status/rw", RouteStatusReadWritable)
	router.HandleFunc("/status/single", RouteStatusSingle)
	router.HandleFunc("/status/leader", RouteStatusLeader)
	router.HandleFunc("/status/follower", RouteStatusFollower)
	router.HandleFunc("/status/topology", RouteStatusTopology)

	router.HandleFunc("/role/master", RouteRoleMaster)
	router.HandleFunc("/role/replica", RouteRoleReplica)
	router.HandleFunc("/role/replica/", RouteRoleReplicaByLag)
	router.HandleFunc("/role/galera", RouteRoleGalera)

	router.HandleFunc("/read/galera/state", RouteReadGaleraState)
	router.HandleFunc("/read/replication/lag", RouteReadReplicationLag)
	router.HandleFunc("/read/replication/master", RouteReadReplicationMaster)
	router.HandleFunc("/read/replication/replicas_count", RouteReadReplicasCounter)

	portstring := "3307"
	log.Printf("Listening on port %s ...", portstring)

	err2 := http.ListenAndServe(":"+portstring, LogRequests(CheckURL(router)))
	log.Fatal(err2)
}

/*
 *	Middleware layers
 */

// LogRequests Middleware level to log API requests
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		log.Printf(
			"[%s]\t%s\t%s",
			r.Method,
			r.URL.String(),
			time.Since(start),
		)
	})
}

// CheckURL Middleware level to validate requested URI
func CheckURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.String()
		pathLength := len(path)
		matchPath := "/role/replica/"
		matchLength := len(matchPath)

		if strings.Contains(path, matchPath) && pathLength > matchLength {
			lag, _ = strconv.Atoi(strings.Trim(path, matchPath))
		} else if strings.Compare(path, strings.TrimRight(path, "/")) != 0 {
			return
		}

		w.Header().Set(ContentType, ResponseType)

		next.ServeHTTP(w, r)
	})
}

/*
 *	General functions
 */

// int2bool Convert integers to boolean
func int2bool(value int) bool {
	if value != 0 {
		return true
	}

	return false
}

// getUnknownColumns Used to get value from specific column of a range of unknown columns
func getUnknownColumns(rows *sql.Rows, column string) string {
	columns, _ := rows.Columns()
	count := len(columns)
	values := make([]interface{}, count)
	valuePtrs := make([]interface{}, count)

	for rows.Next() {
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		rows.Scan(valuePtrs...)

		for i, col := range columns {

			var value interface{}

			val := values[i]

			b, ok := val.([]byte)

			if ok {
				value = string(b)
			} else {
				value = val
			}

			sNum := value.(string)

			if col == column {
				return sNum
			}
		}
	}

	return ""
}

// generateRandomNum Generate random number from a given range
func generateRandomNum(min int, max int) int {
	interval := (max - min) + 1

	return (rand.Intn(interval) + min)
}

/*
 *	Database functions
 */

// readOnly Check if database is in readonly mode, or not
func readOnly() bool {
	var state string
	var key string

	err := db.QueryRow("show variables like 'read_only'").Scan(&key, &state)

	if state == "OFF" || err != nil {
		return false
	}

	return true
}

// replicaStatus Read database status if it is a replica
func replicaStatus(lagCount int) (bool, int) {
	if lagCount == 0 {
		lagCount = 1<<63 - 1
	}

	rows, err := db.Query("show slave status")

	if err != nil {
		return false, 0
	}

	defer rows.Close()

	secondsBehindMaster := getUnknownColumns(rows, "Seconds_Behind_Master")

	if secondsBehindMaster == "" {
		secondsBehindMaster = "0"
	}

	lag, _ = strconv.Atoi(secondsBehindMaster)

	if lag > 0 {
		if lagCount > lag {
			return true, lag
		}

		return false, lag
	}

	return false, 0
}

// isReplica Get database's master, in case it is a replica
func isReplica() (bool, string) {
	rows, err := db.Query("show slave status")

	if err != nil {
		return false, ""
	}

	defer rows.Close()

	masterHost := getUnknownColumns(rows, "Master_Host")

	if masterHost != "" {
		return true, masterHost
	}

	return false, ""
}

// servingBinlogs ...
func servingBinlogs() int {
	var count int

	err := db.QueryRow(
		"select count(*) as n " +
			"from information_schema.processlist " +
			"where command = 'Binlog Dump'").Scan(&count)

	if err != nil {
		return 0
	}

	return count
}

// galeraClusterState ...
func galeraClusterState() (bool, string) {
	var v string

	err := db.QueryRow(
		"select variable_value as v " +
			"from information_schema.global_status " +
			"where variable_name like 'wsrep_local_state' = 4").Scan(&v)

	if err == sql.ErrNoRows || err != nil {
		return false, ""
	}

	return true, v
}

/*
 * Status routes
 */

// RouteStatusReadOnly ...
func RouteStatusReadOnly(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: readOnly...")
	isReadonly := readOnly()

	if isReadonly {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteStatusReadWritable ...
func RouteStatusReadWritable(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: readable and writable...")
	isReadonly := readOnly()

	if !isReadonly {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteStatusSingle ...
func RouteStatusSingle(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: single...")
	isReadonly := readOnly()
	isReplica, _ := isReplica()
	isServeLogs := int2bool(servingBinlogs())

	if !isReadonly && !isReplica && !isServeLogs {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteStatusLeader ...
func RouteStatusLeader(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: leader...")
	isReplica, _ := isReplica()
	isServeLogs := int2bool(servingBinlogs())

	if !isReplica && isServeLogs {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteStatusFollower ...
func RouteStatusFollower(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: follower...")
	isReplica, _ := isReplica()

	if isReplica {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteStatusTopology ...
func RouteStatusTopology(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database status: topology...")
	isReplica, _ := isReplica()
	replicaStatus, _ := replicaStatus(0)
	isServeLogs := int2bool(servingBinlogs())

	if (!replicaStatus && isServeLogs) || isReplica {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

/*
 * Roles routes
 */

// RouteRoleMaster ...
func RouteRoleMaster(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database role: master...")
	isReadonly := readOnly()
	isReplica, _ := isReplica()

	if !isReadonly && !isReplica {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteRoleReplica ...
func RouteRoleReplica(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database role: replica...")
	isReadonly := readOnly()
	replicaStatus, _ := replicaStatus(0)

	if isReadonly && replicaStatus {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteRoleReplicaByLag ...
func RouteRoleReplicaByLag(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database role: replica by lag...")
	isReadonly := readOnly()
	replicaStatus, _ := replicaStatus(lag)

	if isReadonly && replicaStatus {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteRoleGalera ...
func RouteRoleGalera(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Checking database role: galera...")
	galeraClusterState, _ := galeraClusterState()

	if galeraClusterState {
		w.WriteHeader(200)
		res.Status = true
		res.Content = ""
	} else {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

/*
 * Read routes
 */

// RouteReadGaleraState ...
func RouteReadGaleraState(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Reading database state: galera...")
	galeraClusterState, varValue := galeraClusterState()

	if !galeraClusterState {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	} else {
		w.WriteHeader(200)
		res.Status = true
		res.Content = varValue
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteReadReplicationLag ...
func RouteReadReplicationLag(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Reading database replication: lag...")

	isReplica, _ := isReplica()
	_, lagValue := replicaStatus(0)

	if !isReplica {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	} else {
		w.WriteHeader(200)
		res.Status = true
		res.Content = strconv.Itoa(lagValue)
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteReadReplicationMaster ...
func RouteReadReplicationMaster(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Reading database status: master...")
	isReplica, _ := isReplica()
	_, lagValue := replicaStatus(0)

	if !isReplica {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = ""
	} else {
		w.WriteHeader(200)
		res.Status = true
		res.Content = strconv.Itoa(lagValue)
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}

// RouteReadReplicasCounter ...
func RouteReadReplicasCounter(w http.ResponseWriter, r *http.Request) {
	res := new(HTTPResponse)

	log.Print("Reading counter of database replications...")
	isServeLogs := servingBinlogs()

	if !int2bool(isServeLogs) {
		w.WriteHeader(generateRandomNum(418, 451))
		res.Status = false
		res.Content = "0"
	} else {
		w.WriteHeader(200)
		res.Status = true
		res.Content = strconv.Itoa(isServeLogs)
	}

	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
}
