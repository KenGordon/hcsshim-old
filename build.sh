#!/bin/bash

set -eu

dir=$1

export GOOS=windows
export GOPROXY=off         # Prohibit downloads
export GOFLAGS=-mod=vendor # Build using vendor directory

mkdir -p $dir

go build -o $dir github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1
go build -o $dir github.com/Microsoft/hcsshim/cmd/runhcs
go build -o $dir github.com/Microsoft/hcsshim/cmd/shimdiag
go build -o $dir github.com/Microsoft/hcsshim/cmd/tar2ext4
GOOS=linux go build -o $dir -buildmode=pie github.com/Microsoft/hcsshim/cmd/tar2ext4
go build -o $dir github.com/Microsoft/hcsshim/internal/tools/zapdir
go build -o $dir github.com/Microsoft/hcsshim/internal/tools/grantvmgroupaccess

cd test
go test -c -o $dir/cri-containerd.test.exe github.com/Microsoft/hcsshim/test/cri-containerd --tags functional
