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
	node := s.node
	s.mu.RUnlock()
	target.StartHeight = DefaultRescanStartHeightForHash(network, target.DisplayHash)
	if target.StartHeight > 0 {
		return target, nil
	}
	// Without a height every replayed transaction is stamped unconfirmed and
	// the cursor never moves, so no catch-up would ever run again: the wallet
	// would drift out of date with nothing to say so. The node indexes every
	// header from the network checkpoint to the tip, so a hash on that stretch
	// resolves on its own; anything else needs the height typed in.
	if node != nil {
		if hdr, ok := node.HeaderByHash(target.DisplayHash); ok && hdr.Height > 0 {
			target.StartHeight = hdr.Height
			return target, nil
		}
	}
	return target, fmt.Errorf("start block height required: %s is not a block the node knows the height of", shortTxID(target.DisplayHash))
}

func (s *Service) rescanFromHash(target RescanTarget, rebuild bool) (string, error) {
	// One scan at a time: an automatic catch-up and a manual rescan both walk
	// blocks into the same wallet, and two walks would fight over the cursor.
	if !s.scanMu.TryLock() {
		return "", errors.New("a rescan is already running")
	}
	defer s.scanMu.Unlock()

	// A rescan pulls full blocks for as long as it takes to reach the tip, so
	// the user needs to be able to call it off. Progress up to the last
	// checkpoint is already durable when the cancel lands.
	scanCtx, cancelScan := context.WithCancel(context.Background())
	defer cancelScan()
	s.mu.Lock()
	s.scanCancel = cancelScan
	s.resetScanRateLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.scanCancel = nil
		s.resetScanRateLocked()
		s.mu.Unlock()
	}()

	var original preRebuildState
	if rebuild {
		s.mu.Lock()
		if s.wallet == nil || s.payload == nil {
			s.mu.Unlock()
			return "", errors.New("wallet locked")
		}
		original = preRebuildState{
			utxos:        append([]store.UTXORecord(nil), s.payload.UTXOs...),
			syncedHash:   s.payload.SyncedHash,
			syncedHeight: s.payload.SyncedHeight,
		}
		if _, err := s.wallet.Wipe(context.Background()); err != nil {
			s.mu.Unlock()
			return "", err
		}
		s.payload.UTXOs = nil
		// The wipe threw away everything previously scanned, so the cursor no
		// longer describes the wallet. Rewind it to this rescan's start: if
		// the rebuild dies partway, catch-up resumes from there rather than
		// from a height whose blocks are no longer in the wallet.
		s.payload.SyncedHash = target.DisplayHash
		s.payload.SyncedHeight = target.StartHeight
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

	s.setScanPhase("waiting for peers")
	s.publish(EventStatus, "waiting for peer before rescan")
	deadline := time.Now().Add(time.Minute)
	for node.PeerCount() == 0 {
		if scanCtx.Err() != nil {
			return "", errScanStopped
		}
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
	stats, err := wallet.RescanFromHash(scanCtx, target.Hash, bsvsdk.RescanOptions{
		MaxBlocks:                 1_000_000,
		BlockTimeout:              5 * time.Minute,
		BlockProgressInterval:     30 * time.Second,
		MaxConsecutiveBlockErrors: 5,
		StartHeight:               target.StartHeight,
		CheckpointEvery:           rescanCheckpointEvery,
		Checkpoint: func(snapshot []bsvsdk.UTXO, lastBlock [32]byte) {
			s.saveRescanCheckpoint(wallet, snapshot, node, lastBlock)
		},
		GCEvery: rescanGCEvery,
		Progress: func(progress bsvsdk.RescanProgress) {
			s.setScanPhase(scanPhaseLabel(progress.Phase))
			if progress.Phase == "block" {
				s.noteScanProgress(progress.BlocksFetched)
			}
			if !shouldPublishRescanProgress(progress, lastProgress) {
				return
			}
			lastProgress = time.Now()
			s.publish(EventStatus, formatRescanProgress(progress))
		},
	})
	if err != nil {
		// Whether the scan was stopped or failed, the blocks it replayed are
		// real wallet state, and the cursor already names them (checkpoints
		// advance it every hundred blocks). Keep them: the next catch-up
		// resumes from the cursor. A failed rebuild that replayed nothing is
		// the one case where the wipe is the only thing that happened, and
		// there the pre-rebuild set and cursor go back as they were. Putting
		// the old set back after blocks were replayed would leave UTXOs from
		// one height under a cursor from another.
		if rebuild && (stats == nil || stats.BlocksFetched == 0) {
			s.restoreUTXOsAfterRebuild(original, wallet)
		} else {
			s.recordScanProgress(wallet, runtime, stats)
		}
		if errors.Is(err, context.Canceled) || scanCtx.Err() != nil {
			fetched, replayed := 0, 0
			if stats != nil {
				fetched, replayed = stats.BlocksFetched, stats.TxsReplayed
			}
			msg := fmt.Sprintf("scan stopped: %d blocks, %d transactions", fetched, replayed)
			s.publish(EventStatus, msg)
			return msg, nil
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
		s.advanceSyncCursorLocked(displayBlockHash(stats.StoppedAt), stats.StoppedHeight)
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

// errScanStopped marks a scan the user called off, so callers can tell it
// apart from a scan that failed.
var errScanStopped = errors.New("scan stopped")

// recordScanProgress persists whatever a scan reached — used when it ends
// early, where the blocks already replayed are still real wallet state.
func (s *Service) recordScanProgress(wallet *bsvsdk.Wallet, runtime *bsvsdk.Runtime, stats *bsvsdk.RescanStats) {
	if runtime != nil {
		if _, err := runtime.ReloadStore(context.Background()); err != nil {
			s.publish(EventError, "reload SDK store: "+err.Error())
		}
	}
	s.mu.Lock()
	if s.wallet == wallet && s.payload != nil {
		s.refreshUTXOsLocked()
		if stats != nil {
			s.advanceSyncCursorLocked(displayBlockHash(stats.StoppedAt), stats.StoppedHeight)
		}
		if err := s.saveLocked(); err != nil {
			s.publishLocked(EventError, "save after scan stop: "+err.Error())
		}
	}
	s.mu.Unlock()
}

func (s *Service) saveRescanCheckpoint(activeWallet *bsvsdk.Wallet, snapshot []bsvsdk.UTXO, node *bsvsdk.Node, lastBlock [32]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != activeWallet || s.payload == nil {
		return
	}
	s.applyUTXOSnapshotLocked(snapshot)
	// The SDK retains the header of every block a rescan replays, so the
	// checkpointed hash resolves back to a height without a chain lookup.
	if hash := displayBlockHash(lastBlock); hash != "" && node != nil {
		if hdr, ok := node.HeaderByHash(hash); ok {
			s.advanceSyncCursorLocked(hash, hdr.Height)
		}
	}
	if err := s.saveLocked(); err != nil {
		s.publishLocked(EventError, "rescan checkpoint save: "+err.Error())
	}
}

// advanceSyncCursorLocked moves the wallet's scanned-to marker forward. It
// only ever moves forward: a rescan started from a point behind the cursor
// re-covers ground already scanned, and letting it drag the marker back would
// make the next catch-up redo that same stretch again.
func (s *Service) advanceSyncCursorLocked(hash string, height int32) {
	if s.payload == nil || hash == "" || height <= 0 {
		return
	}
	if height <= s.payload.SyncedHeight {
		return
	}
	s.payload.SyncedHash = hash
	s.payload.SyncedHeight = height
}

// CatchUp scans from the wallet's saved cursor to the chain tip. It is what
// makes a balance correct after the wallet has been closed: while it runs the
// node only learns of payments relayed through the mempool, so anything mined
// in the meantime is invisible until those blocks are replayed. Returns an
// empty string when the wallet has no cursor yet and nothing was scanned.
func (s *Service) CatchUp() (string, error) {
	s.mu.RLock()
	var hash string
	var height int32
	if s.payload != nil {
		hash = s.payload.SyncedHash
		height = s.payload.SyncedHeight
	}
	locked := s.wallet == nil
	s.mu.RUnlock()

	if locked {
		return "", errors.New("wallet locked")
	}
	if hash == "" || height <= 0 {
		return "", nil
	}
	target, err := ParseRescanTarget(hash, height)
	if err != nil {
		return "", fmt.Errorf("catch-up cursor %s: %w", shortTxID(hash), err)
	}
	s.publish(EventStatus, fmt.Sprintf("catching up from h=%d block=%s", height, shortTxID(hash)))
	return s.rescanFromHash(target, false)
}

// displayBlockHash renders an internal-order block hash as the display-order
// hex the wallet file and the rescan dialog both use. Inverse of
// parseDisplayBlockHash.
func displayBlockHash(hash [32]byte) string {
	if hash == ([32]byte{}) {
		return ""
	}
	var out [32]byte
	for i := 0; i < 32; i++ {
		out[i] = hash[31-i]
	}
	return hex.EncodeToString(out[:])
}

// preRebuildState is what a rebuild rescan wipes: the UTXO set and the cursor
// that described it. Restored together or not at all — a set from one height
// under a cursor from another is a wrong balance with nothing to say so.
type preRebuildState struct {
	utxos        []store.UTXORecord
	syncedHash   string
	syncedHeight int32
}

func (s *Service) restoreUTXOsAfterRebuild(original preRebuildState, activeWallet *bsvsdk.Wallet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != activeWallet {
		return
	}
	s.restoreUTXOsLocked(original)
	s.publishLocked(EventStatus, "restored pre-rebuild utxo set after rescan failure")
}

func (s *Service) restoreUTXOsLocked(original preRebuildState) {
	if s.wallet == nil || s.payload == nil {
		return
	}
	s.wallet.ClearUTXOs()
	for _, u := range original.utxos {
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
	s.payload.UTXOs = append([]store.UTXORecord(nil), original.utxos...)
	s.restorePreRebuildCursorLocked(original)
	if err := s.saveLocked(); err != nil {
		s.publishLocked(EventError, "save after restore: "+err.Error())
	}
}

// restorePreRebuildCursorLocked puts the cursor back where it was before a
// rebuild rewound it. The UTXO set is the pre-rebuild one again, so the cursor
// has to describe that set: this is the one place it moves backwards on
// purpose.
func (s *Service) restorePreRebuildCursorLocked(original preRebuildState) {
	if s.payload == nil {
		return
	}
	s.payload.SyncedHash = original.syncedHash
	s.payload.SyncedHeight = original.syncedHeight
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
