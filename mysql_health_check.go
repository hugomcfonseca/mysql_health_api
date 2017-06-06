package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
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

type SlaveStatus struct {
	masterHost    string `string:"ip"`
	masterPort    string `string:"port"`
	secondsMaster string `string:"seconds"`
}

// ResponseType Constant
const ResponseType = "application/json"

// ContentType Constant
const ContentType = "Content-Type"

var db *sql.DB
var lag int

func main() {

	var portstring string

	flag.StringVar(&portstring, "port", "3307", "Listening port")
	flag.Parse()

	cfg, err := ini.Load(os.Getenv("HOME") + "/.my.cnf")

	if err != nil {
		log.Panic(err)
	}

	var dbHost string

	dbUser := cfg.Section("client").Key("user").String()
	dbPass := cfg.Section("client").Key("password").String()

	isSocket := cfg.Section("client").HasKey("socket")

	if isSocket {
		dbHost = "unix(" + cfg.Section("client").Key("socket").String() + ")"
	} else {
		dbHost = cfg.Section("client").Key("hostname").String()
	}

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

// unknownColumns Used to get value from specific column of a range of unknown columns
func unknownColumns(rows *sql.Rows) SlaveStatus {
	columns, _ := rows.Columns()
	count := len(columns)
	values := make([]interface{}, count)
	valuePtrs := make([]interface{}, count)
	res := new(SlaveStatus)

	for rows.Next() {
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		rows.Scan(valuePtrs...)

		for i, col := range columns {

			var value interface{}

			val := values[i]

			b, ok := val.([]byte)

			if b == nil {
				return *res
			}

			if ok {
				value = string(b)
			} else {
				value = val
			}

			sNum := value.(string)

			if col == "Master_Host" {
				res.masterHost = sNum
			} else if col == "Master_Port" {
				res.masterPort = sNum
			} else if col == "Seconds_Behind_Master" {
				res.secondsMaster = sNum
			}
		}
	}

	return *res
}

// routeResponse Used to build response to API requests
func routeResponse(w http.ResponseWriter, httpStatus bool, contents string) {
	res := new(HTTPResponse)

	if httpStatus {
		w.WriteHeader(200)
	} else {
		w.WriteHeader(403)
	}

	res.Status = httpStatus
	res.Content = contents
	response, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s", response)
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
		if strconv.IntSize == 64 {
			lagCount = math.MaxInt64
		} else {
			lagCount = math.MaxInt32
		}
	}

	notSlave := false

	rows, err := db.Query("show slave status")

	if err != nil {
		return false, 0
	}

	defer rows.Close()

	slaveValues := unknownColumns(rows)

	if slaveValues.secondsMaster == "" {
		notSlave = true
		slaveValues.secondsMaster = "0"
	}

	lag, _ = strconv.Atoi(slaveValues.secondsMaster)

	if lag > 0 || !notSlave {
		if lagCount > lag {
			return true, lag
		}

		return false, lag
	}

	return false, 0
}

// isReplica Get database's master, in case it is a replica
func isReplica() (bool, string, string) {
	rows, err := db.Query("show slave status")

	if err != nil {
		return false, "", ""
	}

	defer rows.Close()

	slaveValues := unknownColumns(rows)

	if slaveValues.masterHost != "" {
		return true, slaveValues.masterHost, slaveValues.masterPort
	}

	return false, "", ""
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
	log.Print("Checking database status: readOnly...")
	isReadonly := readOnly()

	routeResponse(w, isReadonly, "")
}

// RouteStatusReadWritable ...
func RouteStatusReadWritable(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database status: readable and writable...")
	isReadonly := readOnly()

	routeResponse(w, !isReadonly, "")
}

// RouteStatusSingle ...
func RouteStatusSingle(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database status: single...")
	isReadonly := readOnly()
	isReplica, _, _ := isReplica()
	isServeLogs := int2bool(servingBinlogs())

	routeResponse(w, !isReadonly && !isReplica && !isServeLogs, "")
}

// RouteStatusLeader ...
func RouteStatusLeader(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database status: leader...")
	isReplica, _, _ := isReplica()
	isServeLogs := int2bool(servingBinlogs())

	routeResponse(w, !isReplica && isServeLogs, "")
}

// RouteStatusFollower ...
func RouteStatusFollower(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database status: follower...")
	isReplica, _, _ := isReplica()

	routeResponse(w, isReplica, "")

}

// RouteStatusTopology ...
func RouteStatusTopology(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database status: topology...")
	isReplica, _, _ := isReplica()
	replicaStatus, _ := replicaStatus(0)
	isServeLogs := int2bool(servingBinlogs())

	routeResponse(w, (!replicaStatus && isServeLogs) || isReplica, "")
}

/*
 * Roles routes
 */

// RouteRoleMaster ...
func RouteRoleMaster(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database role: master...")
	isReadonly := readOnly()
	isReplica, _, _ := isReplica()

	routeResponse(w, !isReadonly && !isReplica, "")
}

// RouteRoleReplica ...
func RouteRoleReplica(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database role: replica...")
	isReadonly := readOnly()
	replicaStatus, _ := replicaStatus(0)

	routeResponse(w, isReadonly && replicaStatus, "")
}

// RouteRoleReplicaByLag ...
func RouteRoleReplicaByLag(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database role: replica by lag...")
	isReadonly := readOnly()
	replicaStatus, _ := replicaStatus(lag)

	routeResponse(w, isReadonly && replicaStatus, "")
}

// RouteRoleGalera ...
func RouteRoleGalera(w http.ResponseWriter, r *http.Request) {
	log.Print("Checking database role: galera...")
	galeraClusterState, _ := galeraClusterState()

	routeResponse(w, galeraClusterState, "")
}

/*
 * Read routes
 */

// RouteReadGaleraState ...
func RouteReadGaleraState(w http.ResponseWriter, r *http.Request) {
	log.Print("Reading database state: galera...")
	galeraClusterState, varValue := galeraClusterState()

	routeResponse(w, galeraClusterState, varValue)
}

// RouteReadReplicationLag ...
func RouteReadReplicationLag(w http.ResponseWriter, r *http.Request) {
	log.Print("Reading database replication: lag...")
	lagString := ""
	isReplica, _, _ := isReplica()
	_, lagValue := replicaStatus(0)

	if isReplica {
		lagString = strconv.Itoa(lagValue)
	}

	routeResponse(w, isReplica, lagString)
}

// RouteReadReplicationMaster ...
func RouteReadReplicationMaster(w http.ResponseWriter, r *http.Request) {
	log.Print("Reading database status: master...")
	isReplica, masterIP, masterPort := isReplica()

	if isReplica {
		masterIP = masterIP + ":" + masterPort
	}

	routeResponse(w, isReplica, masterIP)
}

// RouteReadReplicasCounter ...
func RouteReadReplicasCounter(w http.ResponseWriter, r *http.Request) {
	log.Print("Reading counter of database replications...")
	lagString := "0"
	isServeLogs := servingBinlogs()

	if int2bool(isServeLogs) {
		lagString = strconv.Itoa(isServeLogs)
	}

	routeResponse(w, int2bool(isServeLogs), lagString)
}
