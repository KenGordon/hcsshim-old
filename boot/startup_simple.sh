#!/bin/sh

export PATH="/usr/bin:/usr/local/bin:/bin:/root/bin:/sbin:/usr/sbin:/usr/local/sbin"
export HOME="/root"

/bin/vsockexec -o 2056 -e 2056 echo Running startup_simple.sh
/bin/vsockexec -o 2056 -e 2056 date

/bin/vsockexec -o 2056 -e 2056 echo /init -e 1 /bin/vsockexec -o 2056 -e 109 /bin/gcs -v4 -log-format text
/init -e 1 /bin/vsockexec -o 2056 -e 109 /bin/gcs -v4 -log-format json -loglevel trace -logfile=/tmp/gcs

/bin/vsockexec -o 2056 -e 2056 date

/bin/vsockexec -o 2056 -e 2056 echo Done init

#/bin/vsockexec -o 2056 -e 2056 /bin/snp-report

# need init to have run before top shows much
/bin/vsockexec -o 2056 -e 2056 top -n 1

/bin/vsockexec -o 2056 -e 2056 echo tmp
/bin/vsockexec -o 2056 -e 2056 ls -la /tmp

/bin/vsockexec -o 2056 -e 2056 /bin/dmesg
/bin/vsockexec -o 2056 -e 2056 date

sleep 1
/bin/vsockexec -o 2056 -e 2056 echo DONE




