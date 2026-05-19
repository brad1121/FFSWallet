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
