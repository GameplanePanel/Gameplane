// Builds frpc from the pinned upstream frp release with newer
// golang.org/x/net and golang.org/x/crypto than the upstream binaries ship
// (spec 013 G2, T031). Not part of go.work; tunnel/Dockerfile.frp builds it.
module github.com/GameplanePanel/Gameplane/tunnel/frpc

go 1.26.0

// Mirrors the replace in frp v0.71.0's own go.mod, which does not apply to
// dependents.
replace github.com/hashicorp/yamux => github.com/fatedier/yamux v0.0.0-20250825093530-d0154be01cd6
