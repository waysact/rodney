#!/bin/sh
set -eu

# Run the Go suite, including the cross-process browser integration tests.
go test ./...
