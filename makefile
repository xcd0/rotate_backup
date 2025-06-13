
BIN           := ./rotate_backup.exe
VERSION       := 0.0.2
FLAGS_VERSION := -X main.version=$(VERSION) -X main.revision=$(git rev-parse --short HEAD)
FLAG          := -a -tags netgo -trimpath -ldflags='-s -w -extldflags="-static" $(FLAGS_VERSION) -buildid='

all:
	cat ./makefile
build:
	GOOS=windows go build $(FLAG) -o $(BIN)
run:
	GOOS=windows go run main.go
release:
	GOOS=windows go build $(FLAG) -o $(BIN)
	make upx 
upx:
	upx --lzma $(BIN)
