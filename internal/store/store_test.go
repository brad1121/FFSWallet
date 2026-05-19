package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.json")
	payload := DefaultPayload("test")
	payload.Mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	payload.Addresses = []AddressRecord{{
		Branch:    0,
		Index:     0,
		Address:   "mqT9T4Yh8nZt8nZt8nZt8nZt8nZt8nZt8n",
		ScriptHex: "76a914000000000000000000000000000000000000000088ac",
	}}

	kdf := KDF{Algorithm: "argon2id", Time: 1, MemoryKB: 1024, Threads: 1, KeyBytes: 32}
	if err := SaveWithKDF(path, "passphrase", payload, kdf); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load(path, "passphrase")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Mnemonic != payload.Mnemonic {
		t.Fatalf("mnemonic mismatch")
	}
	if got.Network != "test" {
		t.Fatalf("network: got %q", got.Network)
	}
	if got.NextExternal != 1 {
		t.Fatalf("next external: got %d", got.NextExternal)
	}
}

func TestSaveUsesDefaultKDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.json")
	payload := DefaultPayload("test")
	payload.Mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	if err := Save(path, "passphrase", payload); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path, "passphrase")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Mnemonic != payload.Mnemonic {
		t.Fatalf("mnemonic: got %q want %q", loaded.Mnemonic, payload.Mnemonic)
	}
}

func TestLoadAndSaveRejectInvalidInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.json")
	if _, err := Load(path, ""); err == nil {
		t.Fatal("expected passphrase error")
	}
	if err := SaveWithKDF(path, "", DefaultPayload("test"), DefaultKDF()); err == nil {
		t.Fatal("expected save passphrase error")
	}
	if err := SaveWithKDF(path, "pass", nil, DefaultKDF()); err == nil {
		t.Fatal("expected nil payload error")
	}
	if err := SaveWithKDF(path, "pass", DefaultPayload("test"), KDF{Algorithm: "nope", Time: 1, MemoryKB: 1, Threads: 1, KeyBytes: 32}); err == nil {
		t.Fatal("expected unsupported kdf error")
	}
}

func TestLoadRejectsWrongPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.json")
	payload := DefaultPayload("test")
	payload.Mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	kdf := KDF{Algorithm: "argon2id", Time: 1, MemoryKB: 1024, Threads: 1, KeyBytes: 32}
	if err := SaveWithKDF(path, "correct", payload, kdf); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := Load(path, "wrong")
	if !errors.Is(err, ErrInvalidPassphrase) {
		t.Fatalf("expected ErrInvalidPassphrase, got %v", err)
	}
}

func TestWalletNamesAndPaths(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.MkdirAll(WalletsDir(baseDir), 0o700); err != nil {
		t.Fatalf("mkdir wallets dir: %v", err)
	}
	for _, name := range []string{"alice", "bob"} {
		path := WalletPath(baseDir, name)
		if err := SaveWithKDF(path, "passphrase", DefaultPayload("test"), KDF{Algorithm: "argon2id", Time: 1, MemoryKB: 1024, Threads: 1, KeyBytes: 32}); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	if err := SaveWithKDF(LegacyPath(baseDir), "passphrase", DefaultPayload("test"), KDF{Algorithm: "argon2id", Time: 1, MemoryKB: 1024, Threads: 1, KeyBytes: 32}); err != nil {
		t.Fatalf("save legacy: %v", err)
	}
	names, err := ListWalletNames(baseDir)
	if err != nil {
		t.Fatalf("list wallet names: %v", err)
	}
	want := []string{"alice", "bob", "default"}
	if len(names) != len(want) {
		t.Fatalf("wallet count: got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("wallet names: got %v want %v", names, want)
		}
	}
	if got := WalletPath(baseDir, "default"); got != LegacyPath(baseDir) {
		t.Fatalf("default wallet path: got %q want legacy path", got)
	}
}

func TestWalletAuxiliaryPaths(t *testing.T) {
	baseDir := t.TempDir()
	if got, want := WalletStorePath(baseDir, "alice"), filepath.Join(WalletsDir(baseDir), "alice.sqlite"); got != want {
		t.Fatalf("store path: got %q want %q", got, want)
	}
	if got, want := WalletSnapshotPath(baseDir, "alice"), filepath.Join(WalletsDir(baseDir), "alice.addresses.gob.gz"); got != want {
		t.Fatalf("snapshot path: got %q want %q", got, want)
	}
}

func TestNormalizeWalletName(t *testing.T) {
	if _, err := NormalizeWalletName(""); err == nil {
		t.Fatal("expected empty wallet name error")
	}
	if _, err := NormalizeWalletName("my-wallet_01"); err != nil {
		t.Fatalf("valid wallet name rejected: %v", err)
	}
	if _, err := NormalizeWalletName("bad name"); err == nil {
		t.Fatal("expected invalid wallet name error")
	}
}

func TestPayloadEnsureDefaults(t *testing.T) {
	p := &Payload{Addresses: []AddressRecord{{Branch: 0, Index: 4}, {Branch: 1, Index: 2}}}
	p.EnsureDefaults()
	if p.Version != PayloadVersion || p.WalletName != "default" || p.Network != "test" {
		t.Fatalf("defaults: %#v", p)
	}
	if p.FeePerByte != 1 || p.MaxPeers != 16 {
		t.Fatalf("fee/peers defaults: %#v", p)
	}
	if p.NextExternal != 5 || p.NextChange != 3 {
		t.Fatalf("next indices: external=%d change=%d", p.NextExternal, p.NextChange)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("created_at not defaulted")
	}
}
