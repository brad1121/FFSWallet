package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

func TestServiceTrafficTogglePublishesOnChange(t *testing.T) {
	svc := NewService(t.TempDir())
	if svc.P2PTrafficEnabled() {
		t.Fatal("traffic logging enabled by default")
	}

	svc.SetP2PTrafficEnabled(true)
	if !svc.P2PTrafficEnabled() {
		t.Fatal("traffic logging not enabled")
	}

	select {
	case ev := <-svc.Events():
		if ev.Type != EventStatus || ev.Message != "p2p traffic logging enabled" {
			t.Fatalf("event: got %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestServiceCreateLockUnlockRegtest(t *testing.T) {
	svc := NewService(t.TempDir())
	defer svc.Close()

	mnemonic, err := svc.CreateWallet("alice", "passphrase", NetworkRegtest)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if mnemonic == "" {
		t.Fatal("empty mnemonic")
	}
	created := svc.Snapshot()
	if !created.Unlocked || created.WalletName != "alice" || created.Network != NetworkRegtest || created.ReceiveAddress == "" {
		t.Fatalf("created snapshot: %#v", created)
	}
	if len(created.Addresses) != 1 || created.Addresses[0].Branch != 0 || created.Addresses[0].Index != 0 {
		t.Fatalf("created addresses: %#v", created.Addresses)
	}

	addr, err := svc.NewReceiveAddress()
	if err != nil {
		t.Fatalf("new receive: %v", err)
	}
	if addr == "" {
		t.Fatal("empty receive address")
	}
	if err := svc.SetFeePerByte(2); err != nil {
		t.Fatalf("set fee: %v", err)
	}

	svc.Lock()
	if svc.Snapshot().Unlocked {
		t.Fatal("wallet still unlocked after lock")
	}
	if err := svc.Unlock("passphrase"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	unlocked := svc.Snapshot()
	if !unlocked.Unlocked || unlocked.FeePerByte != 2 || len(unlocked.Addresses) != 2 {
		t.Fatalf("unlocked snapshot: %#v", unlocked)
	}
}

func TestServiceSnapshotLockedAndUnlocked(t *testing.T) {
	svc := NewService(t.TempDir())
	locked := svc.Snapshot()
	if locked.Unlocked || locked.BalanceSats != 0 {
		t.Fatalf("locked snapshot: got %#v", locked)
	}

	svc.payload = &store.Payload{
		WalletName:   "alice",
		Network:      NetworkTestnet,
		FeePerByte:   2,
		Addresses:    []store.AddressRecord{{Branch: 0, Index: 2, Address: "addr2"}},
		UTXOs:        []store.UTXORecord{{Value: 5}, {Value: 7}},
		History:      []store.TxRecord{{TxID: "tx"}},
		NextExternal: 3,
	}
	svc.path = "/tmp/wallet.json"

	snap := svc.Snapshot()
	if snap.Unlocked {
		t.Fatal("snapshot should be locked without wallet runtime")
	}
	if snap.BalanceSats != 12 {
		t.Fatalf("balance: got %d want 12", snap.BalanceSats)
	}
	if snap.ReceiveAddress != "addr2" || snap.WalletName != "alice" || snap.FeePerByte != 2 {
		t.Fatalf("snapshot metadata: got %#v", snap)
	}
}

func TestServicePayloadHelpers(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = store.DefaultPayload(NetworkTestnet)

	svc.addAddressRecordLocked(store.AddressRecord{Branch: 0, Index: 0, Address: "a0"})
	svc.addAddressRecordLocked(store.AddressRecord{Branch: 0, Index: 0, Address: "duplicate"})
	svc.addAddressRecordLocked(store.AddressRecord{Branch: 1, Index: 2, Address: "c2"})

	if len(svc.payload.Addresses) != 2 {
		t.Fatalf("address count: got %d want 2", len(svc.payload.Addresses))
	}
	if svc.payload.NextExternal != 1 || svc.payload.NextChange != 3 {
		t.Fatalf("next indices: external=%d change=%d", svc.payload.NextExternal, svc.payload.NextChange)
	}

	svc.appendHistoryLocked(store.TxRecord{TxID: "tx", Direction: "out", Address: "a", Amount: -1})
	svc.appendHistoryLocked(store.TxRecord{TxID: "tx", Direction: "out", Address: "a", Amount: -2})
	if len(svc.payload.History) != 1 || svc.payload.History[0].Amount != -2 {
		t.Fatalf("history upsert failed: %#v", svc.payload.History)
	}
}

func TestServiceApplyUTXOSnapshotPreservesSeenAt(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = store.DefaultPayload(NetworkTestnet)
	seenAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	svc.payload.UTXOs = []store.UTXORecord{{TxID: "b", Vout: 1, Value: 5, SeenAt: seenAt}}

	svc.applyUTXOSnapshotLocked([]bsvsdk.UTXO{
		{TxID: "c", Vout: 0, Value: 7, Script: []byte{0x51}, Height: -1},
		{TxID: "b", Vout: 1, Value: 6, Script: []byte{0x52}, Height: 12},
	})

	if len(svc.payload.UTXOs) != 2 {
		t.Fatalf("utxo count: got %d", len(svc.payload.UTXOs))
	}
	if svc.payload.UTXOs[0].TxID != "b" || svc.payload.UTXOs[0].SeenAt != seenAt || svc.payload.UTXOs[0].ScriptHex != "52" {
		t.Fatalf("preserved utxo: %#v", svc.payload.UTXOs[0])
	}
	if svc.payload.UTXOs[1].TxID != "c" || svc.payload.UTXOs[1].SeenAt.IsZero() || svc.payload.UTXOs[1].ScriptHex != "51" {
		t.Fatalf("new utxo: %#v", svc.payload.UTXOs[1])
	}
}

func TestServicePublicErrorPathsWithoutRuntime(t *testing.T) {
	svc := NewService(t.TempDir())
	if _, err := svc.Send("", 1); err == nil {
		t.Fatal("expected empty send destination error")
	}
	if _, err := svc.Send("addr", 0); err == nil {
		t.Fatal("expected invalid send amount error")
	}
	if _, err := svc.Send("addr", 1); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("send locked error: %v", err)
	}
	if _, err := svc.SendAll(""); err == nil {
		t.Fatal("expected empty sweep destination error")
	}
	if _, err := svc.SendAll("addr"); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("sweep locked error: %v", err)
	}
	if _, err := svc.RebroadcastPending(); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("rebroadcast locked error: %v", err)
	}
	if err := svc.AddPeer(""); err == nil {
		t.Fatal("expected empty peer error")
	}
	if err := svc.AddPeer("127.0.0.1:8333"); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("add peer locked error: %v", err)
	}
	if _, err := svc.RescanFromBlockHashAtHeight(DefaultRescanStartHash(NetworkTestnet), 0); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("rescan locked error: %v", err)
	}
	if _, err := svc.RebuildFromBlockHashAtHeight(DefaultRescanStartHash(NetworkTestnet), 0); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("rebuild locked error: %v", err)
	}
}

func TestServiceSelectionAndWalletMetadata(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewService(baseDir)
	if svc.HasWallets() {
		t.Fatal("unexpected wallets in empty dir")
	}
	if err := svc.SelectWallet("missing"); err == nil {
		t.Fatal("expected missing wallet error")
	}
	path := store.WalletPath(baseDir, "alice")
	if err := store.SaveWithKDF(path, "pass", store.DefaultPayload(NetworkTestnet), store.KDF{Algorithm: "argon2id", Time: 1, MemoryKB: 1024, Threads: 1, KeyBytes: 32}); err != nil {
		t.Fatalf("save wallet: %v", err)
	}
	if !svc.HasWallets() {
		t.Fatal("expected wallet after save")
	}
	if err := svc.SelectWallet("alice"); err != nil {
		t.Fatalf("select wallet: %v", err)
	}
	if svc.WalletName() != "alice" || svc.WalletPath() != path || !svc.WalletExists() {
		t.Fatalf("selected metadata: name=%q path=%q exists=%v", svc.WalletName(), svc.WalletPath(), svc.WalletExists())
	}
}

func TestSetFeePerByteValidation(t *testing.T) {
	svc := NewService(t.TempDir())
	if err := svc.SetFeePerByte(0); err == nil {
		t.Fatal("expected invalid fee error")
	}
	if err := svc.SetFeePerByte(1); err == nil || err.Error() != "wallet locked" {
		t.Fatalf("locked fee error: %v", err)
	}
}

func TestServiceRuntimeConfigUsesWalletPaths(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewService(baseDir)
	payload := store.DefaultPayload(NetworkTestnet)
	payload.WalletName = "alice"
	payload.Mnemonic = "seed words"
	payload.FeePerByte = 3
	payload.MaxPeers = 21

	cfg := svc.runtimeConfig(payload)
	if cfg.WalletName != "alice" || cfg.Mnemonic != "seed words" || cfg.FeePerByte != 3 || cfg.MaxPeers != 21 {
		t.Fatalf("runtime config values: %#v", cfg)
	}
	if cfg.StorePath != store.WalletStorePath(baseDir, "alice") {
		t.Fatalf("store path: got %q", cfg.StorePath)
	}
	if cfg.SnapshotPath != store.WalletSnapshotPath(baseDir, "alice") {
		t.Fatalf("snapshot path: got %q", cfg.SnapshotPath)
	}
}

func TestFormattingHelpers(t *testing.T) {
	if got := normalizeMnemonic("  one\n two\tthree  "); got != "one two three" {
		t.Fatalf("mnemonic: got %q", got)
	}
	if got := shortTxID("1234567890abcdefXYZ1234567890abc"); got != "12345678...67890abc" {
		t.Fatalf("short txid: got %q", got)
	}
	if got := currentReceiveAddress([]store.AddressRecord{{Branch: 0, Index: 1, Address: "a1"}, {Branch: 1, Index: 9, Address: "change"}, {Branch: 0, Index: 2, Address: "a2"}}); got != "a2" {
		t.Fatalf("receive address: got %q", got)
	}
	if got := utxoKey("tx", 4); got != "tx:4" {
		t.Fatalf("utxo key: got %q", got)
	}

	reject := formatReject(bsvsdk.Reject{Peer: "peer", Command: "tx", Code: 0x10, CodeName: "invalid", Hash: "abc"})
	if !strings.Contains(reject, "tx=abc") || !strings.Contains(reject, "no reason") {
		t.Fatalf("reject format: %q", reject)
	}
	traffic := formatP2PTraffic(bsvsdk.P2PTraffic{Peer: "peer", Direction: "in", Command: "tx", PayloadBytes: 42, Summary: "txid=abc"})
	if traffic != "p2p in peer=peer cmd=tx bytes=42 txid=abc" {
		t.Fatalf("traffic format: %q", traffic)
	}
}

func TestNetworkAndRescanFormattingHelpers(t *testing.T) {
	if NormalizeNetwork("mainnet") != NetworkMainnet || NormalizeNetwork("reg") != NetworkRegtest || NormalizeNetwork("unknown") != NetworkTestnet {
		t.Fatal("network normalization mismatch")
	}
	if NetworkLabel(NetworkMainnet) != "Mainnet" || NetworkLabel(NetworkSTN) != "STN" || NetworkLabel(NetworkRegtest) != "Regtest" || NetworkLabel("bad") != "Testnet" {
		t.Fatal("network label mismatch")
	}
	if DefaultRescanStartLabel(NetworkMainnet) == "" || DefaultRescanStartLabel(NetworkRegtest) != "No default checkpoint for this network" {
		t.Fatal("rescan label mismatch")
	}
	progress := bsvsdk.RescanProgress{Phase: "block", Peer: "peer", HeadersSeen: 2, BatchHeaders: 2, BlocksFetched: 100, TxsReplayed: 30, Errors: 1, BlockHash: "abcdef0123456789abcdef0123456789", Message: "ok"}
	formatted := formatRescanProgress(progress)
	for _, want := range []string{"rescan block", "peer=peer", "headers=2", "blocks=100", "txs=30", "errors=1", "batch_headers=2", "block=abcdef01...23456789", `msg="ok"`} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("progress %q missing %q", formatted, want)
		}
	}
	if !shouldPublishRescanProgress(bsvsdk.RescanProgress{Phase: "start"}, time.Now()) {
		t.Fatal("start progress should publish")
	}
	if !shouldPublishRescanProgress(bsvsdk.RescanProgress{Phase: "block", BlocksFetched: 100}, time.Now()) {
		t.Fatal("100th block progress should publish")
	}
	if shouldPublishRescanProgress(bsvsdk.RescanProgress{Phase: "block_request"}, time.Now()) {
		t.Fatal("fresh block request should be throttled")
	}
}

func TestClonePayloadDeepCopy(t *testing.T) {
	src := store.DefaultPayload(NetworkTestnet)
	src.Addresses = []store.AddressRecord{{Address: "original"}}
	clone := clonePayload(src)
	clone.Addresses[0].Address = "changed"

	if src.Addresses[0].Address != "original" {
		t.Fatalf("source mutated: %#v", src.Addresses)
	}
}

// TestNoteScanProgressMeasuresOverWindow pins the rate as a moving-window
// measurement: the first sample only anchors, nothing is reported until a
// window has actually elapsed, and the answer reflects that window rather than
// the whole run.
func TestNoteScanProgressMeasuresOverWindow(t *testing.T) {
	svc := NewService(t.TempDir())

	svc.noteScanProgress(0)
	if svc.scanRate != 0 {
		t.Fatalf("rate reported from the anchoring sample: %v", svc.scanRate)
	}

	// Too soon: still inside the window, so the anchor must not move.
	svc.noteScanProgress(10)
	if svc.scanRate != 0 {
		t.Fatalf("rate reported inside the window: %v", svc.scanRate)
	}

	// Backdate the anchor so a full window has passed with 200 blocks done.
	svc.scanAnchorAt = time.Now().Add(-10 * time.Second)
	svc.scanAnchorBlocks = 0
	svc.noteScanProgress(200)
	if svc.scanRate < 19 || svc.scanRate > 21 {
		t.Fatalf("rate = %v, want about 20 blocks/sec", svc.scanRate)
	}

	// A stalled scan must not keep reporting the old rate as fact once a new
	// window closes with no progress — the anchor moves, the rate holds until
	// there is something to measure.
	svc.scanAnchorAt = time.Now().Add(-10 * time.Second)
	before := svc.scanRate
	svc.noteScanProgress(200)
	if svc.scanRate != before {
		t.Fatalf("rate changed with no blocks replayed: %v -> %v", before, svc.scanRate)
	}

	svc.resetScanRateLocked()
	if svc.scanRate != 0 || !svc.scanAnchorAt.IsZero() {
		t.Fatalf("reset left state behind: rate=%v anchor=%v", svc.scanRate, svc.scanAnchorAt)
	}
}

// TestScanPhaseLabelBlocksAreSilent pins the one case that matters for the
// status line: while blocks are replaying the label is empty, because the
// measured rate describes that better than a word would. Every other phase is
// a state the scan can sit in without blocks moving, and has to say so.
func TestScanPhaseLabelBlocksAreSilent(t *testing.T) {
	if got := scanPhaseLabel("block"); got != "" {
		t.Fatalf("block phase label = %q, want empty", got)
	}
	for phase, want := range map[string]string{
		"start":           "starting",
		"headers_request": "fetching headers",
		"headers":         "fetching headers",
		"block_request":   "requesting blocks",
		"block_wait":      "waiting for a block",
		"block_error":     "retrying a block",
		"peer_switch":     "switching peer",
		"complete":        "finishing",
	} {
		if got := scanPhaseLabel(phase); got != want {
			t.Fatalf("scanPhaseLabel(%q) = %q, want %q", phase, got, want)
		}
	}
	// An unknown phase from a newer SDK is shown as-is rather than swallowed.
	if got := scanPhaseLabel("something_new"); got != "something_new" {
		t.Fatalf("unknown phase = %q, want it passed through", got)
	}
}

func TestCatchUpErrorRetriable(t *testing.T) {
	if catchUpErrorRetriable(nil) {
		t.Fatal("nil error is not retriable")
	}
	if catchUpErrorRetriable(errors.New("wallet locked")) {
		t.Fatal("a locked wallet does not change by waiting")
	}
	if catchUpErrorRetriable(errors.New("a rescan is already running")) {
		t.Fatal("a running scan must not be piled on")
	}
	if !catchUpErrorRetriable(errors.New("rescan: await headers: timeout")) {
		t.Fatal("a peer failure is worth another attempt")
	}
}
