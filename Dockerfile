FROM golang:alpine3.7 as builder

LABEL maintainer='Hugo Fonseca <https://github.com/hugomcfonseca>'

WORKDIR /go/src/github.com/hugomcfonseca/mysql_health_api/

COPY /app/mysql_health_check.go .

RUN apk add --update --no-cache git \
    && go get -d -v \
    && CGO_ENABLED=0 GOOS=linux go build --ldflags '-s' -a -installsuffix cgo -o mysql_health_check .

FROM alpine:3.7

LABEL maintainer='Hugo Fonseca <https://github.com/hugomcfonseca>'

COPY --from=builder /go/src/github.com/hugomcfonseca/mysql_health_api/mysql_health_check /usr/local/bin/

RUN chmod u+x /usr/local/bin/mysql_health_check

ENTRYPOINT [ "mysql_health_check" ]
