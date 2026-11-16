# Port pools for servers behind a port-routed edge

`operator.portPools` (`--port-pool=<template>=<first>-<last>`) gives each
server of a listed template Service ports of its own from a range, a block
per server, kept for its life. Servers reached through a relay or an edge
that routes by port alone, as games whose clients send no hostname must be,
no longer need their ports set by hand.
