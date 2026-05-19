package app

import (
	"testing"

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
