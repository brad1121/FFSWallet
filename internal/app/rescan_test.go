package app

import (
	"testing"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

func TestResolveRescanTargetUsesDefaultCheckpointHeight(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = &store.Payload{Network: NetworkTestnet}

	target, err := svc.resolveRescanTarget(DefaultRescanStartHash(NetworkTestnet), 0)
	if err != nil {
		t.Fatalf("resolve rescan target: %v", err)
	}
	if target.StartHeight != 1713168 {
		t.Fatalf("start height: got %d want 1713168", target.StartHeight)
	}
	if target.DisplayHash != DefaultRescanStartHash(NetworkTestnet) {
		t.Fatalf("display hash normalized: got %q", target.DisplayHash)
	}
}

func TestParseRescanTargetRejectsBadInput(t *testing.T) {
	if _, err := ParseRescanTarget("not-a-hash", 0); err == nil {
		t.Fatal("expected invalid hash error")
	}
	if _, err := ParseRescanTarget(DefaultRescanStartHash(NetworkTestnet), -1); err == nil {
		t.Fatal("expected invalid height error")
	}
}

func TestParseRescanTargetReversesDisplayHash(t *testing.T) {
	input := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	target, err := ParseRescanTarget(input, 123)
	if err != nil {
		t.Fatalf("parse rescan target: %v", err)
	}
	if target.StartHeight != 123 {
		t.Fatalf("start height: got %d want 123", target.StartHeight)
	}
	if target.Hash[0] != 0x1f || target.Hash[31] != 0x00 {
		t.Fatalf("hash byte order not reversed: first=%x last=%x", target.Hash[0], target.Hash[31])
	}
}

func TestDisplayBlockHashRoundTripsParse(t *testing.T) {
	input := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	target, err := ParseRescanTarget(input, 1)
	if err != nil {
		t.Fatalf("parse rescan target: %v", err)
	}
	if got := displayBlockHash(target.Hash); got != input {
		t.Fatalf("round trip: got %q want %q", got, input)
	}
	if got := displayBlockHash([32]byte{}); got != "" {
		t.Fatalf("zero hash: got %q want empty", got)
	}
}

// TestAdvanceSyncCursorOnlyMovesForward pins the invariant the cursor relies
// on: it names the block everything up to which has been scanned. A rescan
// started behind it re-covers scanned ground, and letting that drag the marker
// back would make the next catch-up replay the same stretch again.
func TestAdvanceSyncCursorOnlyMovesForward(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = &store.Payload{Network: NetworkTestnet}

	svc.advanceSyncCursorLocked("aa", 1000)
	if svc.payload.SyncedHeight != 1000 || svc.payload.SyncedHash != "aa" {
		t.Fatalf("first advance: got %q/%d", svc.payload.SyncedHash, svc.payload.SyncedHeight)
	}

	svc.advanceSyncCursorLocked("bb", 900)
	if svc.payload.SyncedHeight != 1000 || svc.payload.SyncedHash != "aa" {
		t.Fatalf("backwards advance took: got %q/%d", svc.payload.SyncedHash, svc.payload.SyncedHeight)
	}

	svc.advanceSyncCursorLocked("cc", 1100)
	if svc.payload.SyncedHeight != 1100 || svc.payload.SyncedHash != "cc" {
		t.Fatalf("forward advance: got %q/%d", svc.payload.SyncedHash, svc.payload.SyncedHeight)
	}

	// A rescan that replayed no blocks reports a zero hash and height -1.
	svc.advanceSyncCursorLocked("", -1)
	if svc.payload.SyncedHeight != 1100 {
		t.Fatalf("empty advance took: got %d", svc.payload.SyncedHeight)
	}
}

// TestCatchUpWithoutCursorDoesNothing covers wallets created before the cursor
// existed: catch-up has no start point, so it must report nothing rather than
// fall back to replaying the chain from the network checkpoint.
func TestCatchUpWithoutCursorDoesNothing(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = &store.Payload{Network: NetworkTestnet}
	svc.wallet = &bsvsdk.Wallet{}

	msg, err := svc.CatchUp()
	if err != nil {
		t.Fatalf("catch up: %v", err)
	}
	if msg != "" {
		t.Fatalf("scanned without a cursor: %q", msg)
	}
}

func TestCatchUpRequiresUnlockedWallet(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = &store.Payload{Network: NetworkTestnet, SyncedHash: DefaultRescanStartHash(NetworkTestnet), SyncedHeight: 1713168}

	if _, err := svc.CatchUp(); err == nil {
		t.Fatal("expected a locked-wallet error")
	}
}
