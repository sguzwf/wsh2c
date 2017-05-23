VERSION := $(shell git describe --tags)
WSH_HTTP_PROXY ?= tcp://127.0.0.1:9999
# This is just used in all, so as to have something in the arch and OS

install:
	go get -d
	go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o ${GOPATH}/bin/wsh

build:
	go get -d
	go build -ldflags "-X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh
# TODO learn for loop in makefile
release:
	go get -d
	GOOS=linux GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_linux_386
	GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_linux_amd64
	GOOS=android GOARCH=arm CGO_ENABLED=1 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_android_arm
	GOOS=windows GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_windows_386.exe
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_windows_amd64.exe
	# GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_darwin_amd64
	# GOOS=darwin GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_darwin_386
	# GOOS=freebsd GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_freebsd_386
	# GOOS=freebsd GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_freebsd_amd64
	# GOOS=freebsd GOARCH=arm go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_freebsd_arm
	# GOOS=netbsd GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_netbsd_386
	# GOOS=netbsd GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_netbsd_amd64
	# GOOS=netbsd GOARCH=arm go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_netbsd_arm
	# GOOS=openbsd GOARCH=386 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_openbsd_386
	# GOOS=openbsd GOARCH=amd64 go build -ldflags "-s -w -X main.versionNumber=${VERSION} -X main.defaultProxy=7777,${WSH_HTTP_PROXY}" -o wsh_openbsd_amd64
	# upx does not work on some arch/OS combos
	upx wsh_*
clean:
	rm wsh*
