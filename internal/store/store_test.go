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

func TestNormalizeWalletName(t *testing.T) {
	if _, err := NormalizeWalletName("my-wallet_01"); err != nil {
		t.Fatalf("valid wallet name rejected: %v", err)
	}
	if _, err := NormalizeWalletName("bad name"); err == nil {
		t.Fatal("expected invalid wallet name error")
	}
}
