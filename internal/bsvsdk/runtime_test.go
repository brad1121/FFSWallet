package bsvsdk

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRuntimeStoreAndAddressSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfg := RuntimeConfig{
		WalletName:   "alice",
		Network:      "test",
		FeePerByte:   1,
		MaxPeers:     16,
		StorePath:    filepath.Join(dir, "wallet.sqlite"),
		SnapshotPath: filepath.Join(dir, "wallet.addresses.gob.gz"),
	}
	rt, mnemonic, err := CreateWallet(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := rt.Wallet.NewDerivedAddress(); err != nil {
		t.Fatalf("derive address: %v", err)
	}
	if err := rt.SaveAddressSnapshot(); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	rt.Close()

	cfg.Mnemonic = mnemonic
	restored, err := RestoreWallet(context.Background(), cfg)
	if err != nil {
		t.Fatalf("restore runtime: %v", err)
	}
	defer restored.Close()
	loaded, err := restored.LoadAddressSnapshot()
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if !loaded {
		t.Fatal("expected snapshot to load")
	}
	if next := restored.Wallet.NextIndex(0); next != 1 {
		t.Fatalf("next external: got %d want 1", next)
	}
	if restored.Store == nil {
		t.Fatal("expected sqlite store")
	}
}

func TestRuntimeStoreWrappersHandleNilRuntime(t *testing.T) {
	var rt *Runtime
	if loaded, err := rt.LoadStore(context.Background()); err != nil || loaded != 0 {
		t.Fatalf("nil LoadStore: loaded=%d err=%v", loaded, err)
	}
	if loaded, err := rt.ReloadStore(context.Background()); err != nil || loaded != 0 {
		t.Fatalf("nil ReloadStore: loaded=%d err=%v", loaded, err)
	}
	if err := rt.SaveAddressSnapshot(); err != nil {
		t.Fatalf("nil SaveAddressSnapshot: %v", err)
	}
	if loaded, err := rt.LoadAddressSnapshot(); err != nil || loaded {
		t.Fatalf("nil LoadAddressSnapshot: loaded=%v err=%v", loaded, err)
	}
}

func TestNetworkTypeAliases(t *testing.T) {
	if networkType("mainnet") != networkType("main") {
		t.Fatal("mainnet alias mismatch")
	}
	if networkType("reg") != networkType("regtest") {
		t.Fatal("regtest alias mismatch")
	}
	if networkType("unknown") != networkType("test") {
		t.Fatal("unknown network should default to testnet")
	}
}

func TestRuntimeMissingSnapshotIsNotError(t *testing.T) {
	rt := &Runtime{SnapshotPath: filepath.Join(t.TempDir(), "missing.gob.gz")}
	loaded, err := rt.LoadAddressSnapshot()
	if err != nil {
		t.Fatalf("load missing snapshot: %v", err)
	}
	if loaded {
		t.Fatal("missing snapshot reported loaded")
	}
}
