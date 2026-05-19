package bsvsdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bsv "github.com/brad1121/bitcoinsv-sdk-go/sdk"
	"github.com/brad1121/bitcoinsv-sdk-go/walletstore/sqlite"
)

type Node = bsv.Node
type Wallet = bsv.Wallet
type Transaction = bsv.Transaction
type OutputSpec = bsv.OutputSpec
type SpendDetail = bsv.SpendDetail
type UTXO = bsv.UTXO
type Reject = bsv.Reject
type P2PTraffic = bsv.P2PTraffic
type OwnedOutput = bsv.OwnedOutput
type SpentOutPoint = bsv.SpentOutPoint
type RescanOptions = bsv.RescanOptions
type RescanProgress = bsv.RescanProgress

type RuntimeConfig struct {
	WalletName   string
	Mnemonic     string
	Network      string
	FeePerByte   int64
	MaxPeers     int
	StorePath    string
	SnapshotPath string
}

type Runtime struct {
	Node         *Node
	Wallet       *Wallet
	Store        *sqlite.Store
	StorePath    string
	SnapshotPath string
}

func CreateWallet(ctx context.Context, cfg RuntimeConfig) (*Runtime, string, error) {
	rt, err := newRuntime(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	wallet, mnemonic, err := rt.Node.CreateWallet(cfg.WalletName)
	if err != nil {
		rt.Close()
		return nil, "", fmt.Errorf("create sdk wallet: %w", err)
	}
	configureWallet(wallet, rt.Store)
	if _, err := wallet.Wipe(ctx); err != nil {
		rt.Close()
		return nil, "", err
	}
	rt.Wallet = wallet
	return rt, mnemonic, nil
}

func RestoreWallet(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	rt, err := newRuntime(ctx, cfg)
	if err != nil {
		return nil, err
	}
	wallet, err := rt.Node.RestoreWallet(cfg.WalletName, cfg.Mnemonic, "")
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("restore sdk wallet: %w", err)
	}
	configureWallet(wallet, rt.Store)
	rt.Wallet = wallet
	return rt, nil
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.Node != nil {
		r.Node.Disconnect()
	}
	if r.Store != nil {
		_ = r.Store.Close()
	}
}

func (r *Runtime) LoadAddressSnapshot() (bool, error) {
	if r == nil || r.Wallet == nil || r.SnapshotPath == "" {
		return false, nil
	}
	nextExternal, nextChange, entries, err := bsv.ReadAddressSnapshot(r.SnapshotPath)
	if err != nil {
		if errors.Is(err, bsv.ErrSnapshotNotFound) {
			return false, nil
		}
		return false, err
	}
	r.Wallet.LoadAddressSnapshot(entries)
	r.Wallet.SetNextIndices(nextExternal, nextChange)
	return true, nil
}

func (r *Runtime) SaveAddressSnapshot() error {
	if r == nil || r.Wallet == nil || r.SnapshotPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.SnapshotPath), 0o700); err != nil {
		return fmt.Errorf("create address snapshot dir: %w", err)
	}
	nextExternal, nextChange := r.Wallet.NextIndices()
	entries := r.Wallet.SaveAddressSnapshot()
	return bsv.WriteAddressSnapshot(r.SnapshotPath, nextExternal, nextChange, entries)
}

func (r *Runtime) LoadStore(ctx context.Context) (int, error) {
	if r == nil || r.Wallet == nil {
		return 0, nil
	}
	return r.Wallet.LoadFromStore(ctx)
}

func (r *Runtime) ReloadStore(ctx context.Context) (int, error) {
	if r == nil || r.Wallet == nil {
		return 0, nil
	}
	return r.Wallet.ReloadFromStore(ctx)
}

func P2PKHOutput(node *Node, addr string, value int64) (OutputSpec, error) {
	return node.P2PKHOutput(addr, value)
}

func newRuntime(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	node := newNode(cfg)
	store, err := openStore(ctx, cfg.StorePath)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Node:         node,
		Store:        store,
		StorePath:    cfg.StorePath,
		SnapshotPath: cfg.SnapshotPath,
	}, nil
}

func configureWallet(wallet *Wallet, store *sqlite.Store) {
	if store != nil {
		wallet.SetStore(store)
	}
	wallet.SetSelector(bsv.SelectorKnapsack)
}

func openStore(ctx context.Context, path string) (*sqlite.Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sdk store dir: %w", err)
	}
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return store, nil
}

func newNode(payload RuntimeConfig) *Node {
	cfg := bsv.DefaultConfig()
	cfg.MaxPeers = payload.MaxPeers
	if cfg.MaxPeers < 16 {
		cfg.MaxPeers = 16
	}
	cfg.FeePerByte = payload.FeePerByte
	cfg.UserAgent = "/ffswallet:0.1/"
	cfg.SkipBlockDownload = true
	return bsv.New(networkType(payload.Network), cfg)
}

func networkType(network string) bsv.NetworkType {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "main", "mainnet":
		return bsv.Mainnet
	case "stn":
		return bsv.STN
	case "regtest", "reg":
		return bsv.Regtest
	default:
		return bsv.Testnet
	}
}
