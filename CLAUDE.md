# FFSWallet

Desktop BSV wallet (Fyne UI) over a private SDK, `bitcoinsv-sdk-go`, checked out
locally and wired in through an ignored `go.work`. CI clones the SDK's default
branch, so **an SDK fix must be pushed before tagging a wallet release**.

## How the wallet learns about money

This is the thing to understand before touching anything balance-related.

The node runs with `SkipBlockDownload = true` (`internal/bsvsdk/runtime.go`). It
follows headers, not blocks. So while it is open it only sees transactions
relayed through the mempool, and it never sees a block. That leaves exactly
three ways a balance becomes correct:

1. **Mempool relay**, via `wallet.OnWalletTx` — live, unconfirmed, only while
   the app is running.
2. **A rescan** (`wallet.RescanFromHash`) — pulls full blocks over P2P and
   replays every transaction. The only path to confirmed history.
3. **Catch-up on unlock** — a rescan from the persisted cursor
   (`Payload.SyncedHash` / `SyncedHeight`) to the tip.

Anything mined while the app was closed is invisible until a rescan replays it.
That is why the cursor exists, and why a rescan that stops early is the most
damaging failure this codebase has.

## The failure mode that keeps coming back

**A rescan that ends early and reports success.** Every incarnation looks
identical from outside: the scan stops, no error appears, the balance is quietly
wrong, and the user is left doing full rebuild rescans from the network
checkpoint to paper over it. It has now been introduced three separate ways:

- **A block announcement read as the getheaders reply.** Peers negotiate
  `sendheaders`, so every new block arrives as a one-header `headers` message on
  the same connection a rescan is walking. Taken as the reply, it jumps the
  cursor to the peer's tip and every block in between is never replayed.
- **An empty headers page read as the getheaders reply.** The manager's
  `pollHeaders` asks the same connection from the live chain tip every 10s, and
  at the tip the answer is an *empty* headers message. An empty page carries no
  header to check against the cursor, so it slipped past the guard written for
  the case above, and the walk concluded "nothing after the cursor" while tens
  of thousands of blocks behind.
- **A stale peer pool.** The parallel prefetch captured `ConnectedPeers()` once
  at scan start. Peers churn constantly; after a few hours the pool held only
  dead connections while healthy peers sat unused.

The lesson: **a headers page must be proven to be the reply to our own
getheaders before it is acted on.** Both the "builds on the cursor" check and
the "empty only means caught up at the tip" check exist for this. Do not relax
either. `awaitHeadersFrom` in `sdk/sdk.go` is the single place this is enforced;
`RescanFromHash` and `ScanBlocks` both go through it.

Related height bugs with the same signature — wrong balance, no error:

- Block heights were a running counter, so one failed block fetch shifted every
  later block in the page one height low, compounding per failure, corrupting
  confirmation state and the `MarkConflicted` cascade. Heights are now
  **positional within their headers page**.
- `handleHeaders` set `headerTip` from every accepted batch, so a rescan's
  historical pages dragged the node's idea of the chain tip *backwards* —
  `ChainHeight` reported the rescan's position, and `pollHeaders` followed the
  rescan through history instead of tracking the chain.
- `Peer.TheirHeight` was written once at the version handshake and never again,
  so `BestPeerHeight` — and the "blocks behind" figure built on it — went stale
  from the moment a peer connected.

## Invariants

Breaking any of these produces a wrong balance with no error:

- **Replay is strictly serial and in chain order.**
  `ProcessTransactionInBlock` stamps heights and drives the `MarkConflicted`
  cascade. Fetching is parallel; applying is not, and must not become so.
- **Dispatch is also in order.** This is what keeps the prefetch window from
  deadlocking: the block replay is waiting for is always requested before any
  block behind it.
- **The cursor only ever advances past blocks actually replayed**, and only ever
  moves forward (`advanceSyncCursorLocked`). A rebuild rescan rewinds it to its
  own start first, because the wipe means the blocks up to the old cursor are no
  longer in the wallet.
- **Header tip and peer heights only move forward.**
- **The prefetch window is bounded by bytes, not just count.** Block sizes vary
  by orders of magnitude; a count alone is not a memory bound.
- **A reject is one peer's opinion.** `RejectDuplicate` (0x12) means the peer
  already had the transaction — that is successful relay, not failure. Only a
  genuine rejection of a transaction we sent discards it. The missing-inputs
  resync deliberately uses a plain rescan, never a rebuild: that path is
  reachable by any peer we broadcast to, and a wipe on demand would be a gift.

## Testing rescans

**Short runs do not catch these bugs.** Every early-stop bug above survived
validation that looked thorough. A useful run must:

- **Cross a headers page boundary (>2000 blocks) while well below the tip.**
  Both reply-misread bugs first bite at the boundary, where the walk waits for a
  reply and something else is on the wire. A 600-block run never gets there.
- **Run long enough for peers to churn** — that is what killed the pool.
- **Compare serial against parallel.** `Prefetch: 1` restores the old
  strictly-serial path. The same range must replay an **identical transaction
  count** and stop at an **identical height** either way. A reordering or
  skipping bug shows up immediately as a different tx count; wall-clock speed
  proves nothing about correctness.

There is a harness pattern for this: a throwaway module with a `replace` to the
local SDK, connecting to testnet, creating a scratch wallet, and rescanning a
fixed range from the Chronicle checkpoint (testnet height 1713168) under
`PREFETCH` and `MAXBLOCKS` env vars. Reach for it before trusting any change to
the rescan path — the unit suite does not exercise the concurrency, and a green
`go test ./...` says nothing about whether a scan reaches the tip.

Also worth knowing: `go test -race ./...` and `gofmt -l` are clean on this repo,
but the SDK has some pre-existing unformatted files (`sdk/sdk_test.go`,
`internal/p2p/{addrman,mempool,message}.go`) — don't sweep those into a diff.

## Release

Tag `vX.Y.Z` on `master`. The Release workflow packages Linux/macOS/Windows,
signs the Linux artifacts with cosign, publishes, and then calls `pages.yml` via
`workflow_call` to mirror the release to GitHub Pages.

Two things that were not obvious:

- **`release: published` never fires for our own releases.** The release is
  created by `action-gh-release` using `GITHUB_TOKEN`, and GitHub does not let
  events generated with that token trigger further workflows. The mirror is
  driven by `workflow_call` from Release for this reason; the `release` trigger
  is only useful for a release published by hand or a PAT.
- **The `github-pages` environment needs a `v*` tag deployment policy.**
  Releases run from a tag ref, and the environment originally allowed only the
  `master` branch, so the deploy job was rejected before any step ran.

Verify a release by simulating CI exactly — fresh clones of both repos (so no
`go.work`), `go mod edit -replace`, `go test ./...`, and a real binary build —
before pushing the tag.
