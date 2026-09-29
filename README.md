# FFSWallet

Bitcoin SV wallet written in Go, for desktop (Linux, macOS, Windows), Android and iOS.

Seed-word imports can optionally rescan from a user-provided block hash. Default start point is the Chronicle checkpoint for supported networks.

## Sync

The node follows the mempool, not the block chain, so a payment mined while the
wallet was closed is not in the balance until its block is replayed. Each
completed rescan records the block it reached, and unlocking the wallet catches
up from there in the background. The status bar shows how far the wallet has
been scanned alongside the peers' best height.

Wallets created before the cursor existed have no start point: run one rescan to
establish it, after which catch-up is automatic.

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

## Mobile

The same app builds for Android and iOS; on a phone the screens stack and the
tabs sit at the bottom. Wallets live in the app's private storage rather than
the user config directory, and returning to the app from the background runs
a catch-up, since a suspended app saw nothing.

The `Mobile` workflow builds both on every push that touches the app, and
Release attaches them to each release:

- `FFSWallet-android.apk`: universal APK (arm, arm64, x86, x86-64), signed with
  the Fyne debug key so any device will sideload it. It is marked debuggable,
  so treat it as a test build. A Play Store build needs your own keystore:
  `fyne release --target android --key-store ... --key-name ...`, which
  produces a signed `.aab`.
- `FFSWallet-ios-simulator-arm64.zip`: an ad-hoc-signed app for the iOS
  Simulator. Running on an iPhone needs an Apple Developer account: import the
  signing certificate and provisioning profile on a Mac, then
  `fyne release --target ios --certificate "Apple Distribution" --profile <name> --app-id com.ffswallet.mobile`.

Each build is installed on an emulator / simulator in CI and must still be
running after launch; the screenshots are kept as workflow artifacts.

Locally, `go run -tags mobile ./cmd/ffswallet` runs the phone layout in a
desktop window.

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
