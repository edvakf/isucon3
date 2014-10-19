#!/bin/bash -x

cd $(dirname $0)

GOPATH=/home/isucon/local/go
go get github.com/go-sql-driver/mysql
go get github.com/gorilla/mux
go get code.google.com/p/go-uuid/uuid
go build -o app
