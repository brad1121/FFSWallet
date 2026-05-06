package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/argon2"
)

var ErrInvalidPassphrase = errors.New("invalid passphrase or corrupt wallet file")

func DefaultKDF() KDF {
	return KDF{
		Algorithm: "argon2id",
		Time:      3,
		MemoryKB:  64 * 1024,
		Threads:   4,
		KeyBytes:  32,
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(dir, "FFSWallet", "wallet.json"), nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func Load(path, passphrase string) (*Payload, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file EncryptedFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse wallet file: %w", err)
	}
	plain, err := decrypt(file, passphrase)
	if err != nil {
		return nil, err
	}
	var payload Payload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, fmt.Errorf("parse wallet payload: %w", err)
	}
	payload.EnsureDefaults()
	return &payload, nil
}

func Save(path, passphrase string, payload *Payload) error {
	return SaveWithKDF(path, passphrase, payload, DefaultKDF())
}

func SaveWithKDF(path, passphrase string, payload *Payload, kdf KDF) error {
	if passphrase == "" {
		return errors.New("passphrase required")
	}
	if payload == nil {
		return errors.New("payload required")
	}
	payload.EnsureDefaults()
	payload.UpdatedAt = time.Now().UTC()

	plain, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet payload: %w", err)
	}
	file, err := encrypt(plain, passphrase, kdf)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create wallet dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write wallet tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace wallet file: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func encrypt(plain []byte, passphrase string, kdf KDF) (*EncryptedFile, error) {
	if kdf.Algorithm == "" {
		kdf = DefaultKDF()
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("random salt: %w", err)
	}
	key, err := deriveKey(passphrase, salt, kdf)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("random nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	return &EncryptedFile{
		Version:    FileVersion,
		KDF:        kdf,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func decrypt(file EncryptedFile, passphrase string) ([]byte, error) {
	if file.Version != FileVersion {
		return nil, fmt.Errorf("unsupported wallet file version %d", file.Version)
	}
	salt, err := base64.StdEncoding.DecodeString(file.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(file.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(file.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	key, err := deriveKey(passphrase, salt, file.KDF)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidPassphrase
	}
	return plain, nil
}

func deriveKey(passphrase string, salt []byte, kdf KDF) ([]byte, error) {
	if kdf.Algorithm != "argon2id" {
		return nil, fmt.Errorf("unsupported kdf %q", kdf.Algorithm)
	}
	if kdf.Time == 0 || kdf.MemoryKB == 0 || kdf.Threads == 0 || kdf.KeyBytes == 0 {
		return nil, errors.New("invalid kdf parameters")
	}
	return argon2.IDKey([]byte(passphrase), salt, kdf.Time, kdf.MemoryKB, kdf.Threads, kdf.KeyBytes), nil
}
