package store

import (
	"errors"
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
