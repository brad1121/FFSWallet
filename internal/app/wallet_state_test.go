package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
	"github.com/brad1121/FFSWallet/internal/store"
)

type txOut struct {
	value  int64
	script []byte
}

// rawTx serialises a transaction spending prev:vout with the given outputs.
// It is unsigned: the wallet's ingest path records ownership and spends, it
// does not validate scripts.
func rawTx(t *testing.T, prev [32]byte, vout uint32, outs ...txOut) []byte {
	t.Helper()
	var raw []byte
	raw = binary.LittleEndian.AppendUint32(raw, 1)
	raw = append(raw, 1)
	raw = append(raw, prev[:]...)
	raw = binary.LittleEndian.AppendUint32(raw, vout)
	raw = append(raw, 0)
	raw = binary.LittleEndian.AppendUint32(raw, 0xffffffff)
	raw = append(raw, byte(len(outs)))
	for _, o := range outs {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(o.value))
		if len(o.script) >= 0xfd {
			t.Fatalf("script too long for a one-byte length: %d", len(o.script))
		}
		raw = append(raw, byte(len(o.script)))
		raw = append(raw, o.script...)
	}
	raw = binary.LittleEndian.AppendUint32(raw, 0)
	return raw
}

// txHash returns a raw transaction's hash in internal byte order and its
// display-order txid.
func txHash(raw []byte) ([32]byte, string) {
	first := sha256.Sum256(raw)
	h := sha256.Sum256(first[:])
	var disp [32]byte
	for i := range h {
		disp[i] = h[31-i]
	}
	return h, hex.EncodeToString(disp[:])
}

// TestGenuineRejectAgreesWithStore pins the fix for a rejected send that was
// undone in memory only. The discard used to put the spent coins back into
// the in-memory set while the SDK store still held the transaction as live,
// so the next LoadFromStore / ReloadFromStore — every unlock, every scan end —
// spent the coins again and dropped the balance with no record of why.
func TestGenuineRejectAgreesWithStore(t *testing.T) {
	svc := NewService(t.TempDir())
	defer svc.Close()
	if _, err := svc.CreateWallet("alice", "passphrase", NetworkRegtest); err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	snap := svc.Snapshot()
	script, err := hex.DecodeString(snap.Addresses[0].ScriptHex)
	if err != nil {
		t.Fatal(err)
	}
	const funded = 100_000
	svc.mu.Lock()
	wallet := svc.wallet
	svc.mu.Unlock()
	fund := rawTx(t, sha256.Sum256([]byte("coinbase")), 0, txOut{funded, script})
	fundHash, fundID := txHash(fund)
	if err := wallet.ProcessRawTx(fund, 10); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if got := wallet.Balance(); got != funded {
		t.Fatalf("funded balance = %d, want %d", got, funded)
	}

	// What a Send leaves behind: the spend ingested into the store and an
	// outgoing history row carrying the inputs it consumed.
	elsewhere := append([]byte{0x76, 0xa9, 0x14}, make([]byte, 20)...)
	elsewhere = append(elsewhere, 0x88, 0xac)
	spend := rawTx(t, fundHash, 0, txOut{10_000, elsewhere}, txOut{89_000, script})
	_, txid := txHash(spend)
	if err := wallet.ProcessRawTx(spend, -1); err != nil {
		t.Fatalf("spend: %v", err)
	}
	if got := wallet.Balance(); got != 89_000 {
		t.Fatalf("balance after send = %d, want 89000 change", got)
	}
	svc.mu.Lock()
	svc.refreshUTXOsLocked()
	svc.appendHistoryLocked(store.TxRecord{
		TxID: txid, Direction: "out", Amount: -10_000, Height: -1, Status: "broadcast",
		RawHex:      hex.EncodeToString(spend),
		SpentInputs: []store.SpentInput{{TxID: fundID, Vout: 0, Value: funded, ScriptHex: hex.EncodeToString(script), Height: 10}},
	})
	svc.mu.Unlock()

	svc.handleReject(bsvsdk.Reject{Command: "tx", Hash: txid, Code: 0x10, Reason: "bad-txns"})

	if got := wallet.Balance(); got != funded {
		t.Fatalf("balance after reject = %d, want %d restored", got, funded)
	}
	// The store must agree, or the next reload quietly re-applies the spend.
	if _, err := wallet.ReloadFromStore(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := wallet.Balance(); got != funded {
		t.Fatalf("balance after reload = %d, want %d: memory and store disagree about the rejected tx", got, funded)
	}
	st, ok, err := wallet.TxState(txid)
	if err != nil || !ok || !st.Conflicted {
		t.Fatalf("store state of rejected tx = %+v ok=%v err=%v, want conflicted", st, ok, err)
	}
}

// TestStoppedRebuildBeforePeersRestoresWallet: a rebuild wipes the wallet and
// rewinds the cursor before it has a peer to scan from. Stopping it while it
// still waits for one replays nothing, so the wipe must be undone — the
// no-peers timeout already did this; a stop left the balance at zero.
func TestStoppedRebuildBeforePeersRestoresWallet(t *testing.T) {
	svc := NewService(t.TempDir())
	defer svc.Close()
	if _, err := svc.CreateWallet("alice", "passphrase", NetworkRegtest); err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	script, err := hex.DecodeString(svc.Snapshot().Addresses[0].ScriptHex)
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	wallet := svc.wallet
	svc.mu.Unlock()
	if err := wallet.ProcessRawTx(rawTx(t, sha256.Sum256([]byte("coinbase")), 0, txOut{50_000, script}), 10); err != nil {
		t.Fatalf("fund: %v", err)
	}
	svc.mu.Lock()
	svc.refreshUTXOsLocked()
	svc.payload.SyncedHash = "ab"
	svc.payload.SyncedHeight = 500
	svc.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := svc.RebuildFromBlockHashAtHeight(strings.Repeat("11", 32), 100)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !svc.ScanRunning() || svc.Snapshot().BalanceSats != 0 {
		if time.Now().After(deadline) {
			t.Fatal("rebuild never reached the wait for peers")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := svc.StopScan(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stopped rebuild reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rebuild did not stop")
	}
	snap := svc.Snapshot()
	if snap.BalanceSats != 50_000 {
		t.Fatalf("balance after stopped rebuild = %d, want 50000 restored", snap.BalanceSats)
	}
	svc.mu.RLock()
	hash, height := svc.payload.SyncedHash, svc.payload.SyncedHeight
	svc.mu.RUnlock()
	if hash != "ab" || height != 500 {
		t.Fatalf("cursor after stopped rebuild = %s/%d, want ab/500", hash, height)
	}
}
