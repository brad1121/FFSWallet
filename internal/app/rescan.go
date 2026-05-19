package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

type RescanTarget struct {
	Hash        [32]byte
	DisplayHash string
	StartHeight int32
}

const (
	rescanCheckpointEvery = 100
	rescanGCEvery         = 100
)

func (s *Service) RescanFromBlockHash(startHash string) (string, error) {
	return s.RescanFromBlockHashAtHeight(startHash, 0)
}

func (s *Service) RescanFromBlockHashAtHeight(startHash string, startHeight int32) (string, error) {
	target, err := s.resolveRescanTarget(startHash, startHeight)
	if err != nil {
		return "", err
	}
	return s.rescanFromHash(target, false)
}

func (s *Service) RebuildFromBlockHash(startHash string) (string, error) {
	return s.RebuildFromBlockHashAtHeight(startHash, 0)
}

func (s *Service) RebuildFromBlockHashAtHeight(startHash string, startHeight int32) (string, error) {
	target, err := s.resolveRescanTarget(startHash, startHeight)
	if err != nil {
		return "", err
	}
	return s.rescanFromHash(target, true)
}

func (s *Service) resolveRescanTarget(startHash string, startHeight int32) (RescanTarget, error) {
	target, err := ParseRescanTarget(startHash, startHeight)
	if err != nil {
		return target, err
	}
	if target.StartHeight > 0 {
		return target, nil
	}
	s.mu.RLock()
	network := ""
	if s.payload != nil {
		network = s.payload.Network
	}
	s.mu.RUnlock()
	target.StartHeight = DefaultRescanStartHeightForHash(network, target.DisplayHash)
	return target, nil
}

func (s *Service) rescanFromHash(target RescanTarget, rebuild bool) (string, error) {
	var original []store.UTXORecord
	if rebuild {
		s.mu.Lock()
		if s.wallet == nil || s.payload == nil {
			s.mu.Unlock()
			return "", errors.New("wallet locked")
		}
		original = append([]store.UTXORecord(nil), s.payload.UTXOs...)
		if _, err := s.wallet.Wipe(context.Background()); err != nil {
			s.mu.Unlock()
			return "", err
		}
		s.payload.UTXOs = nil
		if err := s.saveLocked(); err != nil {
			s.restoreUTXOsLocked(original)
			s.mu.Unlock()
			return "", err
		}
		s.publishLocked(EventStatus, "wallet UTXOs cleared; rebuilding from rescan")
		s.mu.Unlock()
	}

	s.mu.RLock()
	node := s.node
	wallet := s.wallet
	runtime := s.runtime
	s.mu.RUnlock()
	if node == nil || wallet == nil {
		return "", errors.New("wallet locked")
	}

	s.publish(EventStatus, "waiting for peer before rescan")
	deadline := time.Now().Add(time.Minute)
	for node.PeerCount() == 0 {
		if time.Now().After(deadline) {
			err := errors.New("rescan: no connected peers")
			if rebuild {
				s.restoreUTXOsAfterRebuild(original, wallet)
			}
			return "", err
		}
		time.Sleep(time.Second)
	}

	if target.StartHeight > 0 {
		s.publish(EventStatus, fmt.Sprintf("rescan started from h=%d block=%s", target.StartHeight, shortTxID(target.DisplayHash)))
	} else {
		s.publish(EventStatus, "rescan started")
	}
	lastProgress := time.Time{}
	stats, err := wallet.RescanFromHash(context.Background(), target.Hash, bsvsdk.RescanOptions{
		MaxBlocks:                 1_000_000,
		BlockTimeout:              5 * time.Minute,
		BlockProgressInterval:     30 * time.Second,
		MaxConsecutiveBlockErrors: 5,
		StartHeight:               target.StartHeight,
		CheckpointEvery:           rescanCheckpointEvery,
		Checkpoint: func(snapshot []bsvsdk.UTXO, _ [32]byte) {
			s.saveRescanCheckpoint(wallet, snapshot)
		},
		GCEvery: rescanGCEvery,
		Progress: func(progress bsvsdk.RescanProgress) {
			if !shouldPublishRescanProgress(progress, lastProgress) {
				return
			}
			lastProgress = time.Now()
			s.publish(EventStatus, formatRescanProgress(progress))
		},
	})
	if err != nil {
		if rebuild {
			s.restoreUTXOsAfterRebuild(original, wallet)
		}
		s.publish(EventError, err.Error())
		return "", err
	}

	if rebuild {
		if pruned := wallet.PruneUnknownUTXOs(); pruned > 0 {
			s.publish(EventStatus, fmt.Sprintf("rescan pruned %d stale utxo(s)", pruned))
		}
	}
	if runtime != nil {
		if _, err := runtime.ReloadStore(context.Background()); err != nil {
			s.publish(EventError, "reload SDK store: "+err.Error())
		}
	}

	s.mu.Lock()
	active := false
	if s.wallet == wallet && s.payload != nil {
		active = true
		s.refreshUTXOsLocked()
		err = s.saveLocked()
	}
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if active && runtime != nil {
		if err := runtime.SaveAddressSnapshot(); err != nil {
			return "", err
		}
	}

	mode := "rescan"
	if rebuild {
		mode = "rebuild rescan"
	}
	msg := fmt.Sprintf("%s complete: %d blocks, %d transactions", mode, stats.BlocksFetched, stats.TxsReplayed)
	s.publish(EventStatus, msg)
	return msg, nil
}

func (s *Service) saveRescanCheckpoint(activeWallet *bsvsdk.Wallet, snapshot []bsvsdk.UTXO) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != activeWallet || s.payload == nil {
		return
	}
	s.applyUTXOSnapshotLocked(snapshot)
	if err := s.saveLocked(); err != nil {
		s.publishLocked(EventError, "rescan checkpoint save: "+err.Error())
	}
}

func (s *Service) restoreUTXOsAfterRebuild(original []store.UTXORecord, activeWallet *bsvsdk.Wallet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != activeWallet {
		return
	}
	s.restoreUTXOsLocked(original)
	s.publishLocked(EventStatus, "restored pre-rebuild utxo set after rescan failure")
}

func (s *Service) restoreUTXOsLocked(original []store.UTXORecord) {
	if s.wallet == nil || s.payload == nil {
		return
	}
	s.wallet.ClearUTXOs()
	for _, u := range original {
		script, err := hex.DecodeString(u.ScriptHex)
		if err != nil {
			s.publishLocked(EventError, fmt.Sprintf("restore utxo %s:%d decode: %v", u.TxID, u.Vout, err))
			continue
		}
		if err := s.wallet.ForceImportUTXO(u.TxID, u.Vout, u.Value, script, u.Height); err != nil {
			s.publishLocked(EventError, fmt.Sprintf("restore utxo %s:%d import: %v", u.TxID, u.Vout, err))
			continue
		}
	}
	s.payload.UTXOs = append([]store.UTXORecord(nil), original...)
	if err := s.saveLocked(); err != nil {
		s.publishLocked(EventError, "save after restore: "+err.Error())
	}
}

func DefaultRescanStartHash(network string) string {
	switch NormalizeNetwork(network) {
	case NetworkMainnet:
		return "0000000000000000042e91bfd7e7c7ca9eebeb7d6996e4bf010ad8a5b3283efc"
	case NetworkTestnet:
		return "00000000046e321cb1aa6941a7cdc2a97ca65d0caa19e7302df68d1045bd5435"
	default:
		return ""
	}
}

func DefaultRescanStartHeight(network string) int32 {
	switch NormalizeNetwork(network) {
	case NetworkMainnet:
		return 943816
	case NetworkTestnet:
		return 1713168
	default:
		return 0
	}
}

func DefaultRescanStartHeightForHash(network, hash string) int32 {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" || hash != DefaultRescanStartHash(network) {
		return 0
	}
	return DefaultRescanStartHeight(network)
}

func ParseRescanTarget(startHash string, startHeight int32) (RescanTarget, error) {
	var target RescanTarget
	if startHeight < 0 {
		return target, errors.New("start block height must be non-negative")
	}
	hash, display, err := parseDisplayBlockHash(startHash)
	if err != nil {
		return target, err
	}
	target.Hash = hash
	target.DisplayHash = display
	target.StartHeight = startHeight
	return target, nil
}

func DefaultRescanStartLabel(network string) string {
	switch NormalizeNetwork(network) {
	case NetworkMainnet:
		return "Chronicle checkpoint, mainnet height 943816"
	case NetworkTestnet:
		return "Chronicle checkpoint, testnet height 1713168"
	default:
		return "No default checkpoint for this network"
	}
}

func parseDisplayBlockHash(input string) ([32]byte, string, error) {
	var hash [32]byte
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return hash, "", errors.New("start block hash required")
	}
	raw, err := hex.DecodeString(input)
	if err != nil || len(raw) != 32 {
		return hash, "", errors.New("start block hash must be 64 hex characters")
	}
	for i := 0; i < 32; i++ {
		hash[i] = raw[31-i]
	}
	return hash, input, nil
}

func shouldPublishRescanProgress(progress bsvsdk.RescanProgress, last time.Time) bool {
	switch progress.Phase {
	case "start", "headers", "block_error", "complete":
		return true
	case "block_wait":
		return true
	case "block_request":
		return time.Since(last) >= 10*time.Second
	case "block":
		return progress.BlocksFetched == 1 || progress.BlocksFetched%100 == 0 || time.Since(last) >= 10*time.Second
	default:
		return time.Since(last) >= 10*time.Second
	}
}

func formatRescanProgress(progress bsvsdk.RescanProgress) string {
	parts := []string{
		"rescan " + progress.Phase,
		"peer=" + progress.Peer,
		fmt.Sprintf("headers=%d", progress.HeadersSeen),
		fmt.Sprintf("blocks=%d", progress.BlocksFetched),
		fmt.Sprintf("txs=%d", progress.TxsReplayed),
		fmt.Sprintf("errors=%d", progress.Errors),
	}
	if progress.BatchHeaders > 0 {
		parts = append(parts, fmt.Sprintf("batch_headers=%d", progress.BatchHeaders))
	}
	if progress.BlockHash != "" {
		parts = append(parts, "block="+shortTxID(progress.BlockHash))
	}
	if progress.Message != "" {
		parts = append(parts, "msg="+strconv.Quote(progress.Message))
	}
	return strings.Join(parts, " ")
}
