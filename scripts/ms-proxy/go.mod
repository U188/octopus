// strip-proxy is a standalone helper, deliberately kept in its own module so it
// builds in a minimal `golang:alpine` stage with no module downloads (stdlib
// only) and so `go build ./...` at the repository root does not pick it up.
module github.com/U188/octopus/scripts/ms-proxy

go 1.25
