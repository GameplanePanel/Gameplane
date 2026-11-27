# bundlefetch — Specification

**Status:** pre-v1 (spec 043)
**Module / package:** `github.com/GameplanePanel/gameplane/bundlefetch` (packages `oci`, `verify`, `source`)
**Dependencies:** `netguard` (in-repo), oras-go, go-containerregistry, cosign v3, sigstore-go, go-git, go-billy, `golang.org/x/{crypto,mod}`, `go-logr/logr`

## Purpose

The shared code that gets a bundle (a module or a theme) from where it lives and checks it: the OCI client, the cosign verifier core, and the generic git, HTTP archive, local directory and OCI fetch code. The operator's module sources (`operator/internal/modsrc`) and the API's theme sources both use it, so there is one implementation of fetching and of signature checks. It was extracted from `operator/internal/{oci,verify,modsrc}` with no change in module behaviour or messages.

## Responsibilities

1. `oci`: list the semver tags of a repository and pull an artifact. Every byte returned is checked against a digest (the manifest against the returned digest, each layer against the digest the manifest lists), so a signature check on the returned digest covers all returned content. Locale layers (`locales/<tag>.json`) are size-capped per file (`MaxLocaleLayerSize`) and per bundle (`MaxLocaleTotalSize`); a layer over a bound is not read and is recorded with the `OversizedLocale` marker so the caller can report the language.
2. `verify`: a `Verifier` that checks the cosign signature of `ref@digest` under a keyed policy (`NewKeyed`: a PEM public key, optional Rekor inclusion) or a keyless one (`NewKeyless`: Fulcio issuer and identity, trust material from the Sigstore TUF root). `Nop` accepts everything and stands for "no policy". Keys and registry credentials are resolved by the caller and passed in.
3. `source`: fetch the raw files of a bundle source and return them as a filesystem (`Loader`) or a map of file name to bytes, never as CRD types. Git (`GitLoader`, `GitClone`, `CheckGitURL`), HTTP(S) archive (`ArchiveLoader`, `ExtractArchive`, `CheckHTTPURL`), local directory (`LocalLoader`), directory scanning (`FindDirs`, `ReadDir`, `ReadLocaleFiles`, `ContentDigest`, `DigestFiles`, `Allowed`) and OCI listing (`OCIClient`, `OCIRef`, `ListVersions`, `SortVersionsDesc`, `PullFiles`).

## Fetch contract

- A `Loader` returns the filesystem to scan and an optional digest override (`git:<sha>` for git, empty otherwise). It is called afresh on every index and pull.
- A bundle directory is any directory that holds the marker file the caller names (`module.yaml` for modules). `ReadDir` reads the named files plus the locale files, and hashes the directory (`sha256:` over relative path and bytes in lexical order) unless a digest override is given. `DigestFiles` hashes a file map to the same value, so a bundle has one digest whichever source served it.
- Archives are capped at 64 MiB compressed, 256 MiB extracted and 10,000 files; members that escape the extraction root are refused. A locale file over 900 KiB, or past 4 MiB for all languages together, is replaced by `OversizedLocale`.
- Git: only `https://`, `ssh://` and scp-style URLs; clones are shallow and in memory; a branch is tried before a tag; an authenticated clone that is redirected is refused.
- HTTP: `https`, or `http` only with `insecure`; the credential header goes to the first request only, never to a redirect target.
- What a file set means (parsing `module.yaml` or `theme.yaml`, which files are required, name and version rules, de-duplication, allow-list by name) stays with the caller.

## Verify contract

- `Verify(ctx, ref, digest)` returns nil only when `cosign.VerifyImageSignatures` accepts a signature under the policy. A malformed digest is a parse error; an unreachable registry is a verify error.
- Keyed: the signature is complete without a log entry (`IgnoreTlog`, `Offline`, `IgnoreSCT`) unless `requireTlog` is set, in which case the Rekor keys are loaded once at construction and inclusion proofs are checked offline. A failure to reach the Sigstore trust root surfaces at construction.
- Keyless: the Fulcio roots and intermediates come from the Sigstore TUF root; a legacy `TUF_ROOT` is not honoured and logs a loud warning (through the `logr` logger in the context, else `slog`).
- The caller must never send credentials chosen for one registry to another; the operator's `registryVerifier` enforces that around this package.

## netguard at dial time

Every outbound dial goes through `netguard` with the permissive `IsAllowed` policy: the OCI client's HTTP transport, the git HTTPS transport (plus a host pre-flight for ssh), and the archive download client. Cloud-metadata, link-local and multicast addresses are refused at dial time, so a DNS name that rebinds to one is caught too; loopback and private registries stay reachable.

## Non-goals / boundaries

- Imports nothing from Kubernetes (`k8s.io/...`, controller-runtime) or from the operator, so the standalone API can use it. Reading Secrets (pull secrets, cosign keys, git and HTTP credentials) is the caller's job; the operator keeps `internal/oci` (`CredentialFromSecret`), `internal/verify` (`Build`) and the Secret-reading parts of `internal/modsrc` as adapters. cosign and go-containerregistry pull Kubernetes client libraries in transitively; `bundlefetch` itself does not import them.
- Does not parse bundle formats, build catalog entries or decide what to install.
- Does not store anything.

## Directory & package layout

| Path | Role |
|---|---|
| `oci/` | `Client` (`New`, `ListTags`, `Pull`), `CredentialFunc`, bundle media types and layer names, locale layer helpers and budget |
| `verify/` | `Verifier`, `Nop`, `NewKeyed`, `NewKeyless`, the cosign core (`cosignVerifier`, `baseCheckOpts`, `fulcioPools`, `poolsFromCAs`, `tufRootWarning`) |
| `source/` | `Loader`, git, archive, local and OCI helpers, directory scanning and digests |

## Testing

Unit tests only. The `oci` and `verify` tests moved with the code from the operator unchanged (fake in-process registry, offline cosign checks). The operator's `modsrc` tests exercise `source` through the adapters; coverage gate 90 % (`.testcoverage.yml`).
