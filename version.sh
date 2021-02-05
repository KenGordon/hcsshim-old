#!/bin/bash

set -eux

gitVersion=$(git describe --match 'v[0-9]*' --always --long --tags)
timeStamp=$(date --utc +%Y%m%d)
echo cdpx-${gitVersion}-${timeStamp}-COUNTER