#!/bin/bash

go get -v
GOOS=linux GOARCH=amd64 go build -v
