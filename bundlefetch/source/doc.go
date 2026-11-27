// Package source is the generic fetch half of the bundle sources: it gets the
// raw files of a bundle from a git repository, an HTTP(S) archive, a local
// directory or an OCI registry, and hands them back as a filesystem or a plain
// map of file name to bytes. It knows no bundle format: what a module or a
// theme is, which files it needs and how its metadata is parsed stay with the
// caller. Every network dial goes through netguard. The package imports nothing
// from Kubernetes; callers resolve credentials themselves and pass them in.
package source
