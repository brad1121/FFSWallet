package app

import (
	"testing"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

func TestPendingRawTxsKeepsBroadcastAndSeenOutgoing(t *testing.T) {
	history := []store.TxRecord{
		{TxID: "tx1", Direction: "out", Status: "broadcast", RawHex: "aa"},
		{TxID: "tx1", Direction: "out", Status: "seen", RawHex: "bb"},
		{TxID: "tx2", Direction: "out", Status: "seen", RawHex: "cc"},
		{TxID: "tx3", Direction: "out", Status: "confirmed", RawHex: "dd"},
		{TxID: "tx4", Direction: "in", Status: "seen", RawHex: "ee"},
		{TxID: "tx5", Direction: "out", Status: "broadcast"},
	}

	got := pendingRawTxs(history)
	if len(got) != 2 {
		t.Fatalf("pending count: got %d want 2", len(got))
	}
	if got[0].txID != "tx1" || got[0].rawHex != "aa" {
		t.Fatalf("first pending: got %#v", got[0])
	}
	if got[1].txID != "tx2" || got[1].rawHex != "cc" {
		t.Fatalf("second pending: got %#v", got[1])
	}
}

func TestSpendValue(t *testing.T) {
	got := spendValue([]bsvsdk.UTXO{{Value: 11}, {Value: 22}})
	if got != 33 {
		t.Fatalf("spend value: got %d want 33", got)
	}
}

func TestMissingInputsRejectRecognisesNodeReasons(t *testing.T) {
	for _, reason := range []string{
		"missing-inputs",
		"bad-txns-inputs-missingorspent",
		"Missing Inputs",
		"bad-txns-inputs-duplicate",
		"bad-txns-inputs-spent",
	} {
		if !missingInputsReject(reason) {
			t.Fatalf("reason %q not recognised as a missing-inputs rejection", reason)
		}
	}
	for _, reason := range []string{"txn-mempool-conflict", "dust", "insufficient priority", ""} {
		if missingInputsReject(reason) {
			t.Fatalf("reason %q wrongly treated as missing inputs", reason)
		}
	}
}

// TestHandleRejectIgnoresForeignTx guards the discard path: a reject naming a
// transaction we did not send must never touch our history.
func TestHandleRejectIgnoresForeignTx(t *testing.T) {
	svc := NewService(t.TempDir())
	svc.payload = &store.Payload{
		Network: NetworkTestnet,
		History: []store.TxRecord{{TxID: "aaaa", Direction: "out", Status: "broadcast"}},
	}

	svc.handleReject(bsvsdk.Reject{Command: "tx", Hash: "bbbb", Code: 0x10, Reason: "invalid"})

	if len(svc.payload.History) != 1 {
		t.Fatalf("history changed for a foreign reject: %#v", svc.payload.History)
	}
}

// TestHandleRejectDuplicateMarksRelayed pins the inversion that matters most:
// a duplicate reject means the peer already had the transaction, so it is
// evidence the broadcast succeeded and must not discard anything.
func TestHandleRejectDuplicateMarksRelayed(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	svc.path = dir + "/w.json"
	svc.passphrase = "pw"
	svc.payload = &store.Payload{
		Network: NetworkTestnet,
		History: []store.TxRecord{{TxID: "aaaa", Direction: "out", Status: "broadcast"}},
	}

	svc.handleReject(bsvsdk.Reject{Command: "tx", Hash: "aaaa", Code: rejectDuplicate, Reason: "txn-already-known"})

	if len(svc.payload.History) != 1 {
		t.Fatalf("duplicate reject discarded the transaction: %#v", svc.payload.History)
	}
	if got := svc.payload.History[0].Status; got != "seen" {
		t.Fatalf("status = %q, want \"seen\"", got)
	}
}

// TestDiscardRejectedRemovesHistory covers the discard itself. The wallet is
// nil here, so this pins the history half; restoring inputs needs a live SDK
// wallet and is exercised against the network, not in unit tests.
func TestDiscardRejectedRemovesHistory(t *testing.T) {
	svc := NewService(t.TempDir())
	rec := store.TxRecord{TxID: "aaaa", Direction: "out", Status: "broadcast"}
	svc.payload = &store.Payload{
		Network: NetworkTestnet,
		History: []store.TxRecord{
			rec,
			{TxID: "cccc", Direction: "in", Status: "seen"},
		},
	}

	restored := svc.discardRejectedLocked(rec, bsvsdk.Reject{Code: 0x10})

	if restored != 0 {
		t.Fatalf("restored = %d, want 0 with no wallet", restored)
	}
	if len(svc.payload.History) != 1 || svc.payload.History[0].TxID != "cccc" {
		t.Fatalf("history = %#v, want only the incoming record", svc.payload.History)
	}
}

// TestDuplicateMeansRelayed pins the reading of bitcoin-sv's REJECT_DUPLICATE:
// "already known" is a successful relay, "inputs spent" is not.
func TestDuplicateMeansRelayed(t *testing.T) {
	for _, reason := range []string{"txn-already-known", "txn-already-in-mempool", "Txn-Already-Known", ""} {
		if !duplicateMeansRelayed(reason) {
			t.Fatalf("reason %q should read as relayed", reason)
		}
	}
	for _, reason := range []string{"bad-txns-inputs-spent", "bad-txns-inputs-missingorspent"} {
		if duplicateMeansRelayed(reason) {
			t.Fatalf("reason %q wrongly read as relayed", reason)
		}
	}
}

// TestHandleRejectDuplicateInputsSpentDiscards: a duplicate-coded reject that
// says the inputs are spent is a real rejection, not evidence of relay.
func TestHandleRejectDuplicateInputsSpentDiscards(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	svc.path = dir + "/w.json"
	svc.passphrase = "pw"
	svc.payload = &store.Payload{
		Network: NetworkTestnet,
		History: []store.TxRecord{{TxID: "aaaa", Direction: "out", Status: "broadcast"}},
	}

	svc.handleReject(bsvsdk.Reject{Command: "tx", Hash: "aaaa", Code: rejectDuplicate, Reason: "bad-txns-inputs-spent"})

	if len(svc.payload.History) != 0 {
		t.Fatalf("inputs-spent reject kept the transaction: %#v", svc.payload.History)
	}
}
