# FFSWallet

Desktop Bitcoin SV wallet written in Go.

Seed-word imports can optionally rescan from a user-provided block hash. Default start point is the Chronicle checkpoint for supported networks.

## Stack

- UI: Fyne, pure Go desktop widgets.
- BSV: private SDK module `bitcoinsv-sdk-go`.
- Secrets: encrypted wallet file using Argon2id and AES-GCM.
- Default network: BSV testnet.

## Run

```sh
go mod tidy
go run ./cmd/ffswallet
```

Wallet data lives under user config dir:

```text
FFSWallet/wallet.json
```

## Build

```sh
go build -o bin/ffswallet ./cmd/ffswallet
```

Local development can use ignored `go.work` to point at a local SDK checkout.
Do not commit SDK source, `vendor/`, or local `replace` directives.

## Release

GitHub Actions builds release packages for Linux, macOS, and Windows when a `v*` tag is pushed.
Linux artifacts include SHA256 checksums and keyless Sigstore signatures.
Published releases are mirrored to GitHub Pages as public binary downloads, so repo can stay private.
Use semantic version tags. First release tag should be `v0.0.1`.

Required repository secret:

```text
SSH_KEY_SDK
```

Set it to read-only deploy key private half for `bitcoinsv-sdk-go`.

Verify Linux release artifact signatures with `cosign verify-blob`, using matching `.sig` and `.pem` assets.

## Pages

`Pages` workflow builds simple public downloads site from published GitHub releases.
Enable GitHub Pages in repo settings with source set to GitHub Actions.
Pages content contains release binaries and signatures only, not source.
