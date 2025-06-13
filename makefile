
BIN           := ./rotate_backup.exe
VERSION       := 0.0.1
FLAGS_VERSION := -X main.version=$(VERSION) -X main.revision=$(git rev-parse --short HEAD)
FLAG          := -a -tags netgo -trimpath -ldflags='-s -w -extldflags="-static" $(FLAGS_VERSION) -buildid='

all:
	cat ./makefile
build:
	GOOS=windows go build
release:
	GOOS=windows go build $(FLAG)
	make upx 
upx:
	upx --lzma $(BIN)
