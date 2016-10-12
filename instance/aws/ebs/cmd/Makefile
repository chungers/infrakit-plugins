# Used to populate version variable in main package.
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always)
REVISION=$(shell git rev-list -1 HEAD)

DEST?=./bin
BIN_NAME?=$(shell echo ${CURDIR} | awk -F '/cmd' '{print $$1}' | awk -F 'instance/' '{print $$NF}' | sed -e 's/\//-/g')

govendor:
	@echo "+ $@"
	govendor sync

bin: govendor
	@echo "+ $@ - building ${BIN_NAME}"
	go build -o ${DEST}/${BIN_NAME} -ldflags "-X main.Version=$(VERSION) -X main.Revision=$(REVISION)" *.go
