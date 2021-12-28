#!/usr/bin/env bash
pwd
cat testdata/stdout.log &
cat testdata/stderr.log 1>&2
