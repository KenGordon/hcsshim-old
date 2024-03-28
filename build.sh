#!/bin/bash

# CDPx treats any output to stderr as a warning, so redirect output from
# "set -x" to stdout instead of stderr.
BASH_XTRACEFD=1
set -eux

export GOPROXY=off         # Prohibit downloads
export GOFLAGS=-mod=vendor # Build using vendor directory

mkdir -p /source/cdpx-artifacts
cd /source/cdpx-artifacts
make -f ../Makefile out/delta.tar.gz CC=musl-gcc # First make the Linux gcs bits
cp out/delta.tar.gz .
cp ../hack/catcpio.sh .

export GOOS=windows # Now build the Windows bits below

go build github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1
go build github.com/Microsoft/hcsshim/cmd/runhcs
go build github.com/Microsoft/hcsshim/cmd/shimdiag
go build github.com/Microsoft/hcsshim/cmd/tar2ext4
go build github.com/Microsoft/hcsshim/cmd/device-util
go build github.com/Microsoft/hcsshim/cmd/ncproxy
go build github.com/Microsoft/hcsshim/cmd/jobobject-util
GOOS=linux go build -buildmode=pie github.com/Microsoft/hcsshim/cmd/tar2ext4
go build github.com/Microsoft/hcsshim/internal/tools/zapdir
go build github.com/Microsoft/hcsshim/internal/tools/grantvmgroupaccess

cd /source/test
go test -c github.com/Microsoft/hcsshim/test/cri-containerd --tags functional
go build -o sample-logging-driver.exe cri-containerd/helpers/log.go
mv ./cri-containerd.test.exe /source/cdpx-artifacts
mv ./sample-logging-driver.exe /source/cdpx-artifacts
