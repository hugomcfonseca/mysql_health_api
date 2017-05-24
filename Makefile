default: clean static

static:
	CGO_ENABLED=0 GOOS=linux go build --ldflags '-s' -a -installsuffix cgo mysql_health_check.go

docker: static alpine scratch

alpine:
	docker build ./ -f Dockerfile.alpine -t hugomcfonseca/mysql_health_check:alpine

scratch:
	docker build ./ -f Dockerfile.scratch -t hugomcfonseca/mysql_health_check:scratch
	docker build ./ -f Dockerfile.scratch -t hugomcfonseca/mysql_health_check

clean:
	rm -f mysql_health_check
