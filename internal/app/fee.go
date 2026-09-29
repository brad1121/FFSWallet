package app

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
)

// Transaction size figures the SDK prices a send with. Its fee depends only
// on how many inputs and outputs a transaction has, never on which coins, so
// the wallet can work the fee out before anything is signed. The tests hold
// these to what the SDK actually charges.
const (
	txOverheadBytes = 10
	p2pkhInputBytes = 148
	outputBytes     = 34
	dustThreshold   = 546
	// A sweep is priced from its unsigned size plus this much per input
	// for the signature script to come.
	sweepSigScriptBytes = 107
)

// SendPreview is what a send will do, worked out before it is signed: the
// fee the user is asked to accept is the fee the transaction pays.
type SendPreview struct {
	To     string
	Amount int64 // paid to the destination
	Fee    int64
	Change int64 // returned to this wallet; 0 when there is no change output
	Inputs int
	Sweep  bool

	outpoints []bsvsdk.OutPoint
}

// Total is what leaves the wallet: the amount plus the fee.
func (p SendPreview) Total() int64 { return p.Amount + p.Fee }

// ErrFeeChanged means the wallet's coins changed between the preview the user
// accepted and the send, so the transaction would no longer match it.
var ErrFeeChanged = errors.New("the fee changed since it was shown; review the send again")

// PreviewSend selects the coins a send of satoshis to "to" would spend and
// reports the fee it would pay.
func (s *Service) PreviewSend(to string, satoshis int64) (SendPreview, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return SendPreview{}, errors.New("destination address required")
	}
	if satoshis <= 0 {
		return SendPreview{}, errors.New("amount must be positive")
	}
	wallet, node, fee, err := s.sendContext()
	if err != nil {
		return SendPreview{}, err
	}
	if _, err := node.P2PKHOutput(to, satoshis); err != nil {
		return SendPreview{}, err
	}
	p, err := selectCoins(spendableCoins(wallet, node), satoshis, fee)
	if err != nil {
		return SendPreview{}, err
	}
	p.To = to
	return p, nil
}

// PreviewSendAll reports what sweeping every spendable coin to "to" would
// send and what it would pay in fee.
func (s *Service) PreviewSendAll(to string) (SendPreview, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return SendPreview{}, errors.New("destination address required")
	}
	wallet, node, fee, err := s.sendContext()
	if err != nil {
		return SendPreview{}, err
	}
	out, err := node.P2PKHOutput(to, 1)
	if err != nil {
		return SendPreview{}, err
	}
	p, err := sweepPreview(spendableCoins(wallet, node), len(out.Script), fee)
	if err != nil {
		return SendPreview{}, err
	}
	p.To = to
	return p, nil
}

func (s *Service) sendContext() (*bsvsdk.Wallet, *bsvsdk.Node, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.wallet == nil || s.node == nil || s.payload == nil {
		return nil, nil, 0, errors.New("wallet locked")
	}
	fee := s.payload.FeePerByte
	if fee <= 0 {
		fee = 1
	}
	return s.wallet, s.node, fee, nil
}

// coin is a spendable UTXO with its outpoint in internal byte order, which is
// what the SDK sorts and names inputs by.
type coin struct {
	bsvsdk.UTXO
	hash [32]byte
}

// spendableCoins is the SDK's spendable set: every coin but a mining reward
// that has not matured, measured against the higher of the node's tip and
// the highest coin (a rescan can know a later block than the header tip).
func spendableCoins(wallet *bsvsdk.Wallet, node *bsvsdk.Node) []coin {
	utxos := wallet.UTXOs()
	tip := node.ChainHeight()
	for _, u := range utxos {
		if u.Height > tip {
			tip = u.Height
		}
	}
	out := make([]coin, 0, len(utxos))
	for _, u := range utxos {
		if u.IsCoinbase && (u.Height < 0 || tip-u.Height+1 < bsvsdk.CoinbaseMaturity) {
			continue
		}
		h, err := bsvsdk.TxIDFromHex(u.TxID)
		if err != nil {
			continue
		}
		out = append(out, coin{UTXO: u, hash: h})
	}
	return out
}

// sortCoins orders coins the way the SDK's selection walks them: confirmed
// before unconfirmed, confirmed largest first, unconfirmed oldest first, ties
// broken on the outpoint. Selecting in the same order is what makes the SDK,
// given the chosen coins, spend all of them.
func sortCoins(coins []coin) {
	sort.SliceStable(coins, func(i, j int) bool {
		a, b := coins[i], coins[j]
		ac, bc := a.Height >= 0, b.Height >= 0
		if ac != bc {
			return ac
		}
		if ac {
			if a.Value != b.Value {
				return a.Value > b.Value
			}
		} else if a.Height != b.Height {
			return a.Height < b.Height
		}
		if c := bytes.Compare(a.hash[:], b.hash[:]); c != 0 {
			return c < 0
		}
		return a.Vout < b.Vout
	})
}

func sendFee(inputs, outputs int, feePerByte int64) int64 {
	return (txOverheadBytes + int64(inputs)*p2pkhInputBytes + int64(outputs)*outputBytes) * feePerByte
}

// selectCoins takes coins in order until they pay amount plus the fee for a
// transaction of that many inputs — with a change output, or without one
// when what is left over would be dust and goes to the fee instead.
func selectCoins(coins []coin, amount, feePerByte int64) (SendPreview, error) {
	sortCoins(coins)
	var (
		total int64
		have  int64
	)
	for _, c := range coins {
		have += c.Value
	}
	p := SendPreview{Amount: amount}
	for i, c := range coins {
		total += c.Value
		p.outpoints = append(p.outpoints, bsvsdk.OutPoint{Hash: c.hash, Index: c.Vout})
		n := i + 1
		if feeChg := sendFee(n, 2, feePerByte); total >= amount+feeChg {
			p.Inputs, p.Fee = n, feeChg
			p.Change = total - amount - feeChg
			return p, nil
		}
		if feeNoChg := sendFee(n, 1, feePerByte); total >= amount+feeNoChg && total-amount-feeNoChg < dustThreshold {
			p.Inputs = n
			// The dust left over is not a change output; it is fee.
			p.Fee = total - amount
			return p, nil
		}
	}
	return SendPreview{}, fmt.Errorf("insufficient funds: have %d sat spendable, need %d plus fee", have, amount)
}

// sweepPreview prices a transaction spending every coin to one output whose
// script is scriptLen bytes, as the SDK's sweep does.
func sweepPreview(coins []coin, scriptLen int, feePerByte int64) (SendPreview, error) {
	if len(coins) == 0 {
		return SendPreview{}, errors.New("wallet has no spendable coins to send")
	}
	// Sorted so two previews of the same coins compare equal: they come
	// out of a map, in no fixed order.
	sortCoins(coins)
	var total int64
	outpoints := make([]bsvsdk.OutPoint, 0, len(coins))
	for _, c := range coins {
		total += c.Value
		outpoints = append(outpoints, bsvsdk.OutPoint{Hash: c.hash, Index: c.Vout})
	}
	n := len(coins)
	unsigned := 4 + varIntSize(n) + n*41 + varIntSize(1) + 8 + varIntSize(scriptLen) + scriptLen + 4
	fee := int64(unsigned+n*sweepSigScriptBytes) * feePerByte
	if total-fee < dustThreshold {
		return SendPreview{}, fmt.Errorf("sending everything leaves %d sat after the %d sat fee, below the %d sat dust limit", total-fee, fee, dustThreshold)
	}
	return SendPreview{Amount: total - fee, Fee: fee, Inputs: n, Sweep: true, outpoints: outpoints}, nil
}

func varIntSize(n int) int {
	switch {
	case n < 0xfd:
		return 1
	case n <= 0xffff:
		return 3
	case n <= 0xffffffff:
		return 5
	default:
		return 9
	}
}
