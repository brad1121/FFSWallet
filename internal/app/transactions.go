package app

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

func (s *Service) Send(to string, satoshis int64) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("destination address required")
	}
	if satoshis <= 0 {
		return "", errors.New("amount must be positive")
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.RLock()
	wallet := s.wallet
	node := s.node
	runtime := s.runtime
	feePerByte := int64(0)
	if s.payload != nil {
		feePerByte = s.payload.FeePerByte
	}
	s.mu.RUnlock()
	if wallet == nil || node == nil {
		return "", errors.New("wallet locked")
	}
	if err := wallet.CanCover(satoshis, feePerByte, 1); err != nil {
		return "", err
	}
	out, err := node.P2PKHOutput(to, satoshis)
	if err != nil {
		return "", err
	}

	changeIndex := wallet.NextIndex(1)
	detail, err := wallet.SpendToOutputsDetailed([]bsvsdk.OutputSpec{out})
	if err != nil {
		s.mu.Lock()
		if s.wallet == wallet {
			s.resetRuntimeFromPayloadLocked()
		}
		s.mu.Unlock()
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != wallet || s.payload == nil {
		return detail.TxID, nil
	}
	spent := spendValue(detail.SpentUTXOs)
	change := int64(0)
	if detail.ChangeUTXO != nil {
		change = detail.ChangeUTXO.Value
		addr, _ := node.DecodeOutputAddress(detail.ChangeUTXO.Script)
		s.addAddressRecordLocked(store.AddressRecord{
			Branch:    1,
			Index:     changeIndex,
			Address:   addr,
			ScriptHex: hex.EncodeToString(detail.ChangeUTXO.Script),
		})
	}
	s.refreshUTXOsLocked()
	s.appendHistoryLocked(store.TxRecord{
		TxID:      detail.TxID,
		Direction: "out",
		Address:   to,
		Amount:    -satoshis,
		Height:    -1,
		Status:    "broadcast",
		SeenAt:    time.Now().UTC(),
		RawHex:    hex.EncodeToString(detail.RawTx),
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	if runtime != nil && s.runtime == runtime {
		if err := runtime.SaveAddressSnapshot(); err != nil {
			return "", err
		}
	}
	fee := spent - satoshis - change
	if fee < 0 {
		fee = 0
	}
	msg := fmt.Sprintf("broadcast tx=%s amount=%d sat to=%s fee=%d sat inputs=%d change=%d sat peers=%d relay=inv+tx", detail.TxID, satoshis, to, fee, len(detail.SpentUTXOs), change, node.PeerCount())
	s.publishLocked(EventSend, msg)
	return detail.TxID, nil
}

func (s *Service) SendAll(to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("destination address required")
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.RLock()
	wallet := s.wallet
	node := s.node
	s.mu.RUnlock()
	if wallet == nil || node == nil {
		return "", errors.New("wallet locked")
	}
	before := wallet.Balance()
	detail, err := wallet.SendAllDetailed(to)
	if err != nil {
		s.mu.Lock()
		if s.wallet == wallet {
			s.resetRuntimeFromPayloadLocked()
		}
		s.mu.Unlock()
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet != wallet || s.payload == nil {
		return detail.TxID, nil
	}
	s.refreshUTXOsLocked()
	s.appendHistoryLocked(store.TxRecord{
		TxID:      detail.TxID,
		Direction: "out",
		Address:   to,
		Amount:    -before,
		Height:    -1,
		Status:    "broadcast",
		SeenAt:    time.Now().UTC(),
		Note:      "send all",
		RawHex:    hex.EncodeToString(detail.RawTx),
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	s.publishLocked(EventSend, fmt.Sprintf("broadcast sweep tx=%s amount=%d sat to=%s peers=%d relay=inv+tx", detail.TxID, before, to, node.PeerCount()))
	return detail.TxID, nil
}

func (s *Service) RebroadcastPending() (int, error) {
	s.mu.RLock()
	if s.node == nil || s.payload == nil {
		s.mu.RUnlock()
		return 0, errors.New("wallet locked")
	}
	node := s.node
	stored := pendingRawTxs(s.payload.History)
	s.mu.RUnlock()

	if node.PeerCount() == 0 {
		return 0, errors.New("rebroadcast: no connected peers")
	}

	for _, tx := range stored {
		raw, err := hex.DecodeString(tx.rawHex)
		if err != nil {
			s.publish(EventError, fmt.Sprintf("decode raw tx %s: %v", shortTxID(tx.txID), err))
			continue
		}
		if _, err := node.BroadcastRaw(raw); err != nil {
			s.publish(EventError, fmt.Sprintf("rebroadcast %s: %v", shortTxID(tx.txID), err))
		}
	}
	count := node.RebroadcastPendingTransactions()
	if count > 0 {
		s.publish(EventStatus, fmt.Sprintf("rebroadcast pending count=%d peers=%d relay=inv+tx", count, node.PeerCount()))
	}
	return count, nil
}

func (s *Service) seedPendingFromHistory(node *bsvsdk.Node) {
	s.mu.Lock()
	if s.node != node || s.payload == nil || s.pendingSeeded {
		s.mu.Unlock()
		return
	}
	s.pendingSeeded = true
	stored := pendingRawTxs(s.payload.History)
	s.mu.Unlock()

	if len(stored) == 0 {
		return
	}
	var seeded int
	for _, tx := range stored {
		raw, err := hex.DecodeString(tx.rawHex)
		if err != nil {
			s.publish(EventError, fmt.Sprintf("seed pending: decode %s: %v", shortTxID(tx.txID), err))
			continue
		}
		if _, err := node.BroadcastRaw(raw); err != nil {
			s.publish(EventError, fmt.Sprintf("seed pending: %s: %v", shortTxID(tx.txID), err))
			continue
		}
		seeded++
	}
	if seeded > 0 {
		s.publish(EventStatus, fmt.Sprintf("seeded %d pending tx(s) for relay", seeded))
	}
}

func (s *Service) markOutgoingRelayedLocked(txID string) bool {
	changed := false
	for i, rec := range s.payload.History {
		if rec.TxID == txID && rec.Direction == "out" && rec.Status == "broadcast" {
			s.payload.History[i].Status = "seen"
			changed = true
		}
	}
	return changed
}

func spendValue(utxos []bsvsdk.UTXO) int64 {
	var total int64
	for _, u := range utxos {
		total += u.Value
	}
	return total
}

type pendingRawTx struct {
	txID   string
	rawHex string
}

func pendingRawTxs(history []store.TxRecord) []pendingRawTx {
	seen := make(map[string]struct{})
	pending := make([]pendingRawTx, 0)
	for _, tx := range history {
		if tx.Direction != "out" || tx.RawHex == "" {
			continue
		}
		if tx.Status != "broadcast" && tx.Status != "seen" {
			continue
		}
		if _, ok := seen[tx.TxID]; ok {
			continue
		}
		seen[tx.TxID] = struct{}{}
		pending = append(pending, pendingRawTx{txID: tx.TxID, rawHex: tx.RawHex})
	}
	return pending
}
