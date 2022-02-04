#!/bin/ksh

MULTUS=/home/dhill/go/src/multus/multus
T=$(mktemp /tmp/_multus.XXXXXXXXX)
if [ $? != 0 ]; then
   	echo 'multus-backup - failed to create temporary file' | logger 
	exit 1
fi

$($MULTUS backup >$T 2>&1)
if [ $? != 0 ]; then
	echo "multus-backup - multus failed ($?) - see $T" | logger
	exit 1
else
	rm -f $T
fi
