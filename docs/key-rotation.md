# Signing key rotation — Ed25519 → ECDSA P-256 (2026-07)

Gameplane's cosign signing key was rotated in July 2026 from an **Ed25519** key
to an **ECDSA P-256** key. The rotation is cross-signed: the old key signed the
new key, so anyone who trusted the old key can confirm the new one is authorized.

## Why

Enabling **Sigstore Rekor transparency** (a public, append-only log of every
signing event, so misuse of the project key is detectable) requires a key type
the log accepts. The classic Rekor entry format rejects Ed25519
(`unsupported hash algorithm: "SHA-256" not in [SHA-512]`), and cosign's
blob-signing path panics on Ed25519. ECDSA P-256 uses SHA-256 natively and is
the key type cosign generates by default, so it works cleanly with Rekor.

## Keys

| File | Key | Role |
| --- | --- | --- |
| [`signing/cosign.pub`](../signing/cosign.pub) | ECDSA P-256 | **Current** key. Verifies everything signed from the rotation onward. |
| [`signing/2026-07-24-cosign-legacy.pub`](../signing/2026-07-24-cosign-legacy.pub) | Ed25519 | Retired key. Verifies releases published **before** the rotation, and the cross-signature below. |
| [`signing/cosign.pub.legacy-sig`](../signing/cosign.pub.legacy-sig) | — | The old key's signature over the new `signing/cosign.pub` (trust continuity). |

## Verify the cross-signature

Confirm the retired key endorsed the current key. With openssl (portable):

```sh
base64 -d signing/cosign.pub.legacy-sig > /tmp/xsig.bin
openssl pkeyutl -verify -pubin -inkey signing/2026-07-24-cosign-legacy.pub -rawin \
  -in signing/cosign.pub -sigfile /tmp/xsig.bin
# → Signature Verified Successfully
```

Or with cosign (needs a version ≤ v2.4.3 — newer cosign has Ed25519
`verify-blob` regressions):

```sh
cosign verify-blob --key signing/2026-07-24-cosign-legacy.pub --insecure-ignore-tlog \
  --signature signing/cosign.pub.legacy-sig signing/cosign.pub
```

## Verifying artifacts

- **From the rotation onward:** use `signing/cosign.pub` (see
  [`install.md`](install.md#verifying-image-signatures)). New signatures are
  recorded in the public Rekor log.
- **Before the rotation:** use `signing/2026-07-24-cosign-legacy.pub` with
  `--insecure-ignore-tlog=true` (those were offline/keyed, unlogged).
