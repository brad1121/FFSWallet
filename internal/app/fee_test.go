package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// feeWallet is an unlocked regtest wallet funded with the given coins, all
// confirmed at height 10, plus the address of a second wallet to pay.
func feeWallet(t *testing.T, feePerByte int64, values ...int64) (*Service, string) {
	t.Helper()
	svc := NewService(t.TempDir())
	t.Cleanup(svc.Close)
	if _, err := svc.CreateWallet("payer", "pw", NetworkRegtest); err != nil {
		t.Fatal(err)
	}
	if feePerByte != 1 {
		if err := svc.SetFeePerByte(feePerByte); err != nil {
			t.Fatal(err)
		}
	}
	script, err := hex.DecodeString(svc.Snapshot().Addresses[0].ScriptHex)
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.RLock()
	wallet := svc.wallet
	svc.mu.RUnlock()
	for i, v := range values {
		raw := rawTx(t, sha256.Sum256([]byte(fmt.Sprintf("coin-%d", i))), 0, txOut{v, script})
		if err := wallet.ProcessRawTx(raw, 10); err != nil {
			t.Fatal(err)
		}
	}
	payee := NewService(t.TempDir())
	t.Cleanup(payee.Close)
	if _, err := payee.CreateWallet("payee", "pw", NetworkRegtest); err != nil {
		t.Fatal(err)
	}
	return svc, payee.Snapshot().ReceiveAddress
}

func coinTotal(svc *Service) int64 {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	var total int64
	for _, u := range svc.wallet.UTXOs() {
		total += u.Value
	}
	return total
}

// sendWithoutPeers runs a send in a wallet with no peers. The SDK signs the
// transaction and commits it to the wallet before the broadcast fails for want
// of a peer, so what it actually charged can be read off the coins left.
func sendWithoutPeers(t *testing.T, svc *Service, p SendPreview) {
	t.Helper()
	var err error
	if p.Sweep {
		_, err = svc.SendAll(p)
	} else {
		_, err = svc.Send(p)
	}
	if err == nil || err.Error() != "broadcast: no connected peers" {
		t.Fatalf("send: err = %v, want the broadcast to fail for want of peers", err)
	}
}

// TestPreviewFeeIsTheFeeCharged: the fee the send dialog shows is the fee the
// signed transaction pays — with change, without (dust going to the fee), at
// more than one fee rate, across one input and several.
func TestPreviewFeeIsTheFeeCharged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		feePerByte int64
		coins      []int64
		amount     int64
		inputs     int
		change     bool
	}{
		{"one input with change", 1, []int64{100_000, 5_000}, 10_000, 1, true},
		{"two inputs with change", 1, []int64{60_000, 50_000, 1_000}, 100_000, 2, true},
		// Short of the fee for a change output, but the leftover is dust:
		// no change output, and the dust is fee.
		{"dust goes to fee", 1, []int64{10_200}, 10_000, 1, false},
		// Enough for the fee with a change output, so the SDK makes one
		// even though it is below the dust threshold.
		{"sub-dust change", 1, []int64{10_300}, 10_000, 1, true},
		{"higher fee rate", 5, []int64{40_000, 30_000, 20_000}, 68_500, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := feeWallet(t, tc.feePerByte, tc.coins...)
			// Paying this wallet's own address keeps the payment among its
			// coins, so only the fee leaves. (A failed send with no output
			// back to the wallet leaves nothing to measure.)
			p, err := svc.PreviewSend(svc.Snapshot().ReceiveAddress, tc.amount)
			if err != nil {
				t.Fatal(err)
			}
			if p.Inputs != tc.inputs || (p.Change > 0) != tc.change {
				t.Fatalf("preview %+v: want %d inputs, change %v", p, tc.inputs, tc.change)
			}
			before := coinTotal(svc)
			sendWithoutPeers(t, svc, p)
			charged := before - coinTotal(svc)
			if charged != p.Fee {
				t.Fatalf("fee charged %d sat, preview showed %d sat", charged, p.Fee)
			}
		})
	}
}

// TestPreviewSweepFeeIsTheFeeCharged: the same for sending everything. The
// sweep pays this wallet's own address, so the output it created shows what
// the destination was sent.
func TestPreviewSweepFeeIsTheFeeCharged(t *testing.T) {
	svc, _ := feeWallet(t, 2, 40_000, 30_000, 20_000)
	own := svc.Snapshot().ReceiveAddress
	p, err := svc.PreviewSendAll(own)
	if err != nil {
		t.Fatal(err)
	}
	if p.Inputs != 3 || p.Amount+p.Fee != 90_000 {
		t.Fatalf("sweep preview %+v", p)
	}
	sendWithoutPeers(t, svc, p)
	if got := coinTotal(svc); got != p.Amount {
		t.Fatalf("sweep delivered %d sat, preview said %d (fee %d)", got, p.Amount, p.Fee)
	}
}

// TestSendRefusesWhenCoinsChanged: a coin arriving between the preview and
// the confirm changes which coins the send would spend, so it is refused
// rather than sent at a fee the user never saw.
func TestSendRefusesWhenCoinsChanged(t *testing.T) {
	svc, payee := feeWallet(t, 1, 20_000, 15_000)
	p, err := svc.PreviewSend(payee, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	script, err := hex.DecodeString(svc.Snapshot().Addresses[0].ScriptHex)
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.RLock()
	wallet := svc.wallet
	svc.mu.RUnlock()
	if err := wallet.ProcessRawTx(rawTx(t, sha256.Sum256([]byte("late")), 0, txOut{500_000, script}), 11); err != nil {
		t.Fatal(err)
	}
	before := coinTotal(svc)
	if _, err := svc.Send(p); !errors.Is(err, ErrFeeChanged) {
		t.Fatalf("send after coins changed: err = %v, want ErrFeeChanged", err)
	}
	if got := coinTotal(svc); got != before {
		t.Fatalf("coins changed from %d to %d on a refused send", before, got)
	}
}
