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
		TxID:        detail.TxID,
		Direction:   "out",
		Address:     to,
		Amount:      -satoshis,
		Height:      -1,
		Status:      "broadcast",
		SeenAt:      time.Now().UTC(),
		RawHex:      hex.EncodeToString(detail.RawTx),
		SpentInputs: spentInputs(detail.SpentUTXOs),
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
		TxID:        detail.TxID,
		Direction:   "out",
		Address:     to,
		Amount:      -before,
		Height:      -1,
		Status:      "broadcast",
		SeenAt:      time.Now().UTC(),
		Note:        "send all",
		RawHex:      hex.EncodeToString(detail.RawTx),
		SpentInputs: spentInputs(detail.SpentUTXOs),
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	s.publishLocked(EventSend, fmt.Sprintf("broadcast sweep tx=%s amount=%d sat to=%s peers=%d relay=inv+tx", detail.TxID, before, to, node.PeerCount()))
	return detail.TxID, nil
}

func (s *Service) RebroadcastPending() (int, error) {
	s.mu.Lock()
	if s.node == nil || s.payload == nil {
		s.mu.Unlock()
		return 0, errors.New("wallet locked")
	}
	node := s.node
	if s.syncPendingStatusLocked() {
		if err := s.saveLocked(); err != nil {
			s.publishLocked(EventError, err.Error())
		}
	}
	stored := pendingRawTxs(s.payload.History)
	s.mu.Unlock()

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
	if s.syncPendingStatusLocked() {
		if err := s.saveLocked(); err != nil {
			s.publishLocked(EventError, err.Error())
		}
	}
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

// syncPendingStatusLocked brings the status of stored outgoing transactions
// in line with what the wallet store knows. History only ever learned
// "broadcast" and "seen", so a transaction that had long since confirmed, or
// that lost to a confirmed double-spend, stayed pending forever — and every
// unlock and every Rebroadcast pushed it to peers again. A confirmed
// transaction becomes "confirmed" at its height; one the store has ruled out
// becomes "conflicted". Both drop out of pendingRawTxs. Reports whether
// anything changed so the caller can save.
func (s *Service) syncPendingStatusLocked() bool {
	if s.wallet == nil || s.payload == nil {
		return false
	}
	states := make(map[string]bsvsdk.TxState)
	for _, rec := range s.payload.History {
		if rec.Direction != "out" || (rec.Status != "broadcast" && rec.Status != "seen") {
			continue
		}
		if _, done := states[rec.TxID]; done {
			continue
		}
		st, ok, err := s.wallet.TxState(rec.TxID)
		if err != nil || !ok || (!st.Confirmed && !st.Conflicted) {
			continue
		}
		states[rec.TxID] = st
	}
	changed := false
	for i := range s.payload.History {
		rec := &s.payload.History[i]
		st, ok := states[rec.TxID]
		if !ok {
			continue
		}
		switch {
		case st.Confirmed:
			if rec.Status != "confirmed" || rec.Height != st.Height {
				rec.Status = "confirmed"
				rec.Height = st.Height
				changed = true
			}
		case st.Conflicted:
			if rec.Status != "conflicted" && rec.Status != "abandoned" {
				rec.Status = "conflicted"
				changed = true
			}
		}
	}
	return changed
}

// AbandonTransaction gives up on an outgoing transaction that has not
// confirmed. The wallet marks it and everything built on it as dead, stops
// announcing it, and returns the coins it spent to the balance.
//
// This exists because the network does not always say no. A conflict is
// detected when a confirmed transaction takes one of our inputs, but a
// transaction whose input simply does not exist — its parent was rejected
// and forgotten, or never relayed — has no competitor. Peers hold it as an
// orphan without a reject, it never confirms, and its change stands in the
// balance until someone gives up on it. A confirmed transaction is refused.
func (s *Service) AbandonTransaction(txID string) error {
	txID = strings.ToLower(strings.TrimSpace(txID))
	if txID == "" {
		return errors.New("transaction id required")
	}
	// Hold the send lock too: a Send in flight may be building on the very
	// coins this returns, and the two must not interleave.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.payload == nil {
		return errors.New("wallet locked")
	}
	s.syncPendingStatusLocked()
	// The transaction may be known only by the change it paid us — an
	// outgoing record is lost when a rejected parent is discarded, and a
	// stuck child of it then has only "in" rows. Any row will do; the store
	// decides whether it is still pending.
	var inputs int
	found := false
	for _, rec := range s.payload.History {
		if !strings.EqualFold(rec.TxID, txID) {
			continue
		}
		found = true
		if !Abandonable(rec) {
			return fmt.Errorf("transaction %s is %s; only a pending transaction can be abandoned", shortTxID(txID), rec.Status)
		}
		if rec.Direction == "out" {
			inputs = len(rec.SpentInputs)
		}
	}
	if !found {
		return fmt.Errorf("transaction %s is not one of ours", shortTxID(txID))
	}
	if err := s.wallet.AbandonTransaction(txID); err != nil {
		return err
	}
	for i := range s.payload.History {
		if strings.EqualFold(s.payload.History[i].TxID, txID) {
			s.payload.History[i].Status = "abandoned"
		}
	}
	// Anything that spent this transaction's outputs is conflicted now.
	s.syncPendingStatusLocked()
	s.refreshUTXOsLocked()
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.publishLocked(EventStatus, fmt.Sprintf("abandoned tx=%s inputs=%d balance=%d sat", txID, inputs, s.wallet.Balance()))
	return nil
}

// Abandonable reports whether a history row describes a transaction that is
// still waiting on the network: an outgoing one that is broadcast or seen, or
// an incoming one that has no height yet. Anything confirmed, conflicted or
// already abandoned is not.
func Abandonable(rec store.TxRecord) bool {
	switch rec.Direction {
	case "out":
		return rec.Status == "broadcast" || rec.Status == "seen"
	case "in":
		return rec.Height < 0 && rec.Status == "seen"
	}
	return false
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

func spentInputs(utxos []bsvsdk.UTXO) []store.SpentInput {
	if len(utxos) == 0 {
		return nil
	}
	out := make([]store.SpentInput, 0, len(utxos))
	for _, u := range utxos {
		out = append(out, store.SpentInput{
			TxID:      u.TxID,
			Vout:      u.Vout,
			Value:     u.Value,
			ScriptHex: hex.EncodeToString(u.Script),
			Height:    u.Height,
		})
	}
	return out
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

// Reject codes we care about. A peer that already has the transaction answers
// with RejectDuplicate — that is successful relay, not a failure.
const (
	rejectDuplicate = 0x12
)

// missingInputsReject reports whether a rejection means the peer could not
// find the coins the transaction spends. That is not a bad transaction: it
// says the wallet's idea of which coins it owns disagrees with the network,
// usually because those coins were already spent somewhere the wallet never
// saw.
func missingInputsReject(reason string) bool {
	reason = strings.ToLower(reason)
	for _, marker := range []string{"missing-inputs", "missing inputs", "missingorspent", "inputs-spent", "bad-txns-inputs"} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

// duplicateMeansRelayed decides what a RejectDuplicate (0x12) is telling us.
// bitcoin-sv uses the same code for two different things: "txn-already-known"
// and "txn-already-in-mempool", which mean the peer has the transaction — a
// successful relay — and "bad-txns-inputs-spent", which means the coins it
// spends are gone. Only the first is good news. A duplicate reject with no
// reason is read as relayed: a reject is one peer's word, and discarding a
// transaction on a guess is worse than keeping a doubtful one.
func duplicateMeansRelayed(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return true
	}
	return strings.Contains(reason, "already")
}

// handleReject reacts to a peer rejecting one of our own broadcasts. A reject
// from a single peer is not proof the network refused the transaction — peers
// reject for local policy reasons, and a duplicate reject means the opposite —
// so only a genuine rejection of a transaction we sent discards it.
func (s *Service) handleReject(reject bsvsdk.Reject) {
	if !strings.EqualFold(strings.TrimSpace(reject.Command), "tx") {
		return
	}
	txID := strings.ToLower(strings.TrimSpace(reject.Hash))
	if txID == "" {
		return
	}

	s.mu.Lock()
	if s.payload == nil {
		s.mu.Unlock()
		return
	}
	rec, found := s.findOutgoingLocked(txID)
	if !found {
		// Not ours — someone else's transaction relayed past us.
		s.mu.Unlock()
		return
	}
	if reject.Code == rejectDuplicate && duplicateMeansRelayed(reject.Reason) {
		// The peer already has it: the broadcast worked.
		if s.markOutgoingRelayedLocked(txID) {
			if err := s.saveLocked(); err != nil {
				s.publishLocked(EventError, err.Error())
			}
		}
		s.mu.Unlock()
		return
	}

	restored := s.discardRejectedLocked(rec, reject)
	if err := s.saveLocked(); err != nil {
		s.publishLocked(EventError, err.Error())
	}
	s.mu.Unlock()

	s.publish(EventError, fmt.Sprintf("transaction rejected by %s: %s (code 0x%02x/%s) — tx=%s discarded, %d input(s) returned to balance",
		reject.Peer, reject.Reason, reject.Code, reject.CodeName, shortTxID(txID), restored))

	if missingInputsReject(reject.Reason) {
		// Peers reject in parallel; one resync is enough, and rescanFromHash
		// would refuse the rest anyway.
		go s.resyncFromRejectedInputs(rec)
	}
}

func (s *Service) findOutgoingLocked(txID string) (store.TxRecord, bool) {
	for _, rec := range s.payload.History {
		if strings.EqualFold(rec.TxID, txID) && rec.Direction == "out" {
			return rec, true
		}
	}
	return store.TxRecord{}, false
}

// discardRejectedLocked removes a rejected transaction from history and puts
// the coins it spent back, so the balance stops counting them as gone.
// Returns how many inputs were restored.
func (s *Service) discardRejectedLocked(rec store.TxRecord, reject bsvsdk.Reject) int {
	kept := s.payload.History[:0]
	for _, h := range s.payload.History {
		if strings.EqualFold(h.TxID, rec.TxID) && h.Direction == "out" {
			continue
		}
		kept = append(kept, h)
	}
	s.payload.History = append([]store.TxRecord(nil), kept...)

	if s.wallet == nil {
		return 0
	}
	// Drop any change output the rejected transaction created: it does not
	// exist, and leaving it would overstate the balance.
	for _, u := range s.payload.UTXOs {
		if strings.EqualFold(u.TxID, rec.TxID) {
			s.wallet.UntrackUTXO(u.TxID, u.Vout)
		}
	}
	restored := 0
	for _, in := range rec.SpentInputs {
		script, err := hex.DecodeString(in.ScriptHex)
		if err != nil {
			s.publishLocked(EventError, fmt.Sprintf("restore input %s:%d decode: %v", shortTxID(in.TxID), in.Vout, err))
			continue
		}
		if err := s.wallet.ForceImportUTXO(in.TxID, in.Vout, in.Value, script, in.Height); err != nil {
			s.publishLocked(EventError, fmt.Sprintf("restore input %s:%d: %v", shortTxID(in.TxID), in.Vout, err))
			continue
		}
		restored++
	}
	s.refreshUTXOsLocked()
	return restored
}

// resyncFromRejectedInputs handles the case where the network says the coins
// we tried to spend are not there. The wallet is out of sync from at least as
// far back as the oldest of those coins, so it rescans from that block rather
// than from the network checkpoint — the wallet already recorded the height it
// believed each coin was created at, and everything from the earliest of them
// forward is what needs re-deriving from chain truth.
func (s *Service) resyncFromRejectedInputs(rec store.TxRecord) {
	s.mu.RLock()
	node := s.node
	network := ""
	if s.payload != nil {
		network = s.payload.Network
	}
	s.mu.RUnlock()
	if node == nil {
		return
	}

	from := int32(-1)
	for _, in := range rec.SpentInputs {
		if in.Height <= 0 {
			// An unconfirmed input we never saw mined: nothing to anchor on.
			continue
		}
		if from < 0 || in.Height < from {
			from = in.Height
		}
	}
	if from < 0 {
		s.publish(EventError, "rejected transaction spent unconfirmed inputs; run a rebuild rescan to resync")
		return
	}
	// Start one block before the earliest disputed coin so the block that
	// created it is replayed too.
	if from > 1 {
		from--
	}

	hdr, ok := node.HeaderByHeight(from)
	if !ok {
		s.publish(EventError, fmt.Sprintf("cannot resolve block at h=%d to resync from; run a rebuild rescan from %s",
			from, DefaultRescanStartLabel(network)))
		return
	}
	s.publish(EventStatus, fmt.Sprintf("inputs missing on network; resyncing wallet from h=%d block=%s", from, shortTxID(hdr.Hash)))
	// A plain rescan, not a rebuild. Replaying the blocks from here sees the
	// transaction that actually spent these coins and marks them spent, which
	// is the whole correction needed — and it reaches that state without
	// wiping wallet state first. It also keeps a peer from being able to cost
	// us our UTXO set: a reject is one peer's word, and this path is reachable
	// by any peer we broadcast to.
	if _, err := s.RescanFromBlockHashAtHeight(hdr.Hash, from); err != nil {
		s.publish(EventError, "resync after rejected inputs: "+err.Error())
	}
}
