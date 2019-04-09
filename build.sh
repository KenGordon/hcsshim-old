#!/bin/bash

# CDPx treats any output to stderr as a warning, so redirect output from
# "set -x" to stdout instead of stderr.
BASH_XTRACEFD=1
set -eux

export GOOS=windows

mkdir -p /source/cdpx-artifacts
cd /source/cdpx-artifacts

# Set up GOPATH with a symlink pointing to the actual source location.
mkdir -p $GOPATH/src/github.com/Microsoft
ln -s /source $GOPATH/src/github.com/Microsoft/hcsshim

go build github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1
go build github.com/Microsoft/hcsshim/cmd/runhcs
go build github.com/Microsoft/hcsshim/cmd/tar2ext4
go build github.com/Microsoft/hcsshim/internal/tools/zapdir
go build github.com/Microsoft/hcsshim/internal/tools/grantvmgroupaccess
go test -c github.com/Microsoft/hcsshim/test/cri-containerd --tags functional