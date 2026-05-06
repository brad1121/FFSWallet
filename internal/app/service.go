package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	bsv "github.com/brad1121/bitcoinsv-sdk-go/sdk"

	"github.com/brad1121/FFSWallet/internal/store"
)

const (
	NetworkMainnet = "main"
	NetworkTestnet = "test"
	NetworkSTN     = "stn"
	NetworkRegtest = "regtest"
)

type EventType string

const (
	EventStatus  EventType = "status"
	EventWallet  EventType = "wallet"
	EventPayment EventType = "payment"
	EventSend    EventType = "send"
	EventError   EventType = "error"
	EventPeer    EventType = "peer"
)

type Event struct {
	Type    EventType
	Message string
	At      time.Time
}

type NodeStatus struct {
	Unlocked       bool
	Connected      bool
	Network        string
	PeerCount      int
	BestPeerHeight int32
	ChainHeight    int32
	LastMessage    string
	LastError      string
}

type Snapshot struct {
	Unlocked       bool
	StorePath      string
	Network        string
	WalletName     string
	FeePerByte     int64
	BalanceSats    int64
	ReceiveAddress string
	Addresses      []store.AddressRecord
	UTXOs          []store.UTXORecord
	History        []store.TxRecord
	Status         NodeStatus
}

type Service struct {
	mu sync.RWMutex

	path       string
	passphrase string
	payload    *store.Payload
	node       *bsv.Node
	wallet     *bsv.Wallet

	events     chan Event
	statusStop chan struct{}
}

func NewService(path string) *Service {
	return &Service{
		path:   path,
		events: make(chan Event, 128),
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(true)
}

func (s *Service) Events() <-chan Event {
	return s.events
}

func (s *Service) WalletPath() string {
	return s.path
}

func (s *Service) WalletExists() bool {
	return store.Exists(s.path)
}

func (s *Service) CreateWallet(passphrase, network string) (string, error) {
	if strings.TrimSpace(passphrase) == "" {
		return "", errors.New("passphrase required")
	}
	network = NormalizeNetwork(network)
	payload := store.DefaultPayload(network)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(false)

	node := newNode(payload)
	wallet, mnemonic, err := node.CreateWallet(payload.WalletName)
	if err != nil {
		return "", fmt.Errorf("create sdk wallet: %w", err)
	}
	payload.Mnemonic = mnemonic

	s.passphrase = passphrase
	s.payload = payload
	s.node = node
	s.wallet = wallet

	if _, err := s.addNewReceiveLocked(); err != nil {
		s.stopRuntimeLocked(true)
		return "", err
	}
	if err := s.saveLocked(); err != nil {
		s.stopRuntimeLocked(true)
		return "", err
	}
	s.wireRuntimeLocked(node, wallet)
	if err := s.connectLocked(); err != nil {
		return "", err
	}
	s.startStatusLoopLocked()
	s.publishLocked(EventWallet, "wallet created")
	return mnemonic, nil
}

func (s *Service) RestoreWallet(mnemonic, passphrase, network string) error {
	return s.createWalletFromMnemonic(mnemonic, passphrase, network, "wallet restored")
}

func (s *Service) CreateWalletFromSeed(seedWords, passphrase, network string) error {
	return s.createWalletFromMnemonic(seedWords, passphrase, network, "wallet created from seed")
}

func (s *Service) createWalletFromMnemonic(mnemonic, passphrase, network, eventMessage string) error {
	if strings.TrimSpace(passphrase) == "" {
		return errors.New("passphrase required")
	}
	mnemonic = normalizeMnemonic(mnemonic)
	if mnemonic == "" {
		return errors.New("mnemonic required")
	}
	network = NormalizeNetwork(network)
	payload := store.DefaultPayload(network)
	payload.Mnemonic = mnemonic

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(false)

	node := newNode(payload)
	wallet, err := node.RestoreWallet(payload.WalletName, mnemonic, "")
	if err != nil {
		return fmt.Errorf("restore sdk wallet: %w", err)
	}
	s.passphrase = passphrase
	s.payload = payload
	s.node = node
	s.wallet = wallet

	if _, err := s.addNewReceiveLocked(); err != nil {
		s.stopRuntimeLocked(true)
		return err
	}
	if err := s.saveLocked(); err != nil {
		s.stopRuntimeLocked(true)
		return err
	}
	s.wireRuntimeLocked(node, wallet)
	if err := s.connectLocked(); err != nil {
		return err
	}
	s.startStatusLoopLocked()
	s.publishLocked(EventWallet, eventMessage)
	return nil
}

func (s *Service) Unlock(passphrase string) error {
	if strings.TrimSpace(passphrase) == "" {
		return errors.New("passphrase required")
	}
	payload, err := store.Load(s.path, passphrase)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(false)
	if err := s.startExistingRuntimeLocked(payload, passphrase); err != nil {
		s.stopRuntimeLocked(true)
		return err
	}
	s.publishLocked(EventWallet, "wallet unlocked")
	return nil
}

func (s *Service) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(true)
	s.publishLocked(EventWallet, "wallet locked")
}

func (s *Service) NewReceiveAddress() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil {
		return "", errors.New("wallet locked")
	}
	addr, err := s.addNewReceiveLocked()
	if err != nil {
		return "", err
	}
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	s.publishLocked(EventWallet, "receive address generated")
	return addr, nil
}

func (s *Service) Send(to string, satoshis int64) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("destination address required")
	}
	if satoshis <= 0 {
		return "", errors.New("amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.node == nil {
		return "", errors.New("wallet locked")
	}
	if err := s.wallet.CanCover(satoshis, s.payload.FeePerByte, 1); err != nil {
		return "", err
	}
	out, err := s.node.P2PKHOutput(to, satoshis)
	if err != nil {
		return "", err
	}

	changeIndex := s.payload.NextChange
	detail, err := s.wallet.SpendToOutputsDetailed([]bsv.OutputSpec{out})
	if err != nil {
		s.resetRuntimeFromPayloadLocked()
		return "", err
	}
	if detail.ChangeUTXO != nil {
		addr, _ := s.node.DecodeOutputAddress(detail.ChangeUTXO.Script)
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
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	s.publishLocked(EventSend, fmt.Sprintf("sent %d sat to %s", satoshis, to))
	return detail.TxID, nil
}

func (s *Service) SendAll(to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("destination address required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil {
		return "", errors.New("wallet locked")
	}
	before := s.wallet.Balance()
	txid, err := s.wallet.SendAll(to)
	if err != nil {
		s.resetRuntimeFromPayloadLocked()
		return "", err
	}
	s.refreshUTXOsLocked()
	s.appendHistoryLocked(store.TxRecord{
		TxID:      txid,
		Direction: "out",
		Address:   to,
		Amount:    -before,
		Height:    -1,
		Status:    "broadcast",
		SeenAt:    time.Now().UTC(),
		Note:      "send all",
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	s.publishLocked(EventSend, fmt.Sprintf("swept %d sat to %s", before, to))
	return txid, nil
}

func (s *Service) RescanFromBlockHash(startHash string) (string, error) {
	hash, err := parseDisplayBlockHash(startHash)
	if err != nil {
		return "", err
	}

	s.mu.RLock()
	node := s.node
	wallet := s.wallet
	s.mu.RUnlock()
	if node == nil || wallet == nil {
		return "", errors.New("wallet locked")
	}

	s.publish(EventStatus, "waiting for peer before rescan")
	deadline := time.Now().Add(time.Minute)
	for node.PeerCount() == 0 {
		if time.Now().After(deadline) {
			return "", errors.New("rescan: no connected peers")
		}
		time.Sleep(time.Second)
	}

	s.publish(EventStatus, "rescan started")
	stats, err := wallet.RescanFromHash(context.Background(), hash, bsv.RescanOptions{MaxBlocks: 1_000_000})
	if err != nil {
		s.publish(EventError, err.Error())
		return "", err
	}

	s.mu.Lock()
	if s.wallet == wallet && s.payload != nil {
		s.refreshUTXOsLocked()
		err = s.saveLocked()
	}
	s.mu.Unlock()
	if err != nil {
		return "", err
	}

	msg := fmt.Sprintf("rescan complete: %d blocks, %d transactions", stats.BlocksFetched, stats.TxsReplayed)
	s.publish(EventStatus, msg)
	return msg, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{
		StorePath: s.path,
		Status:    s.statusLocked(),
	}
	if s.payload == nil {
		return snap
	}
	snap.Unlocked = s.wallet != nil
	snap.Network = s.payload.Network
	snap.WalletName = s.payload.WalletName
	snap.FeePerByte = s.payload.FeePerByte
	snap.ReceiveAddress = currentReceiveAddress(s.payload.Addresses)
	snap.Addresses = append([]store.AddressRecord(nil), s.payload.Addresses...)
	snap.UTXOs = append([]store.UTXORecord(nil), s.payload.UTXOs...)
	snap.History = append([]store.TxRecord(nil), s.payload.History...)
	if s.wallet != nil {
		snap.BalanceSats = s.wallet.Balance()
	} else {
		for _, u := range s.payload.UTXOs {
			snap.BalanceSats += u.Value
		}
	}
	return snap
}

func (s *Service) startExistingRuntimeLocked(payload *store.Payload, passphrase string) error {
	payload.EnsureDefaults()
	node := newNode(payload)
	wallet, err := node.RestoreWallet(payload.WalletName, payload.Mnemonic, "")
	if err != nil {
		return fmt.Errorf("restore sdk wallet: %w", err)
	}

	s.passphrase = passphrase
	s.payload = payload
	s.node = node
	s.wallet = wallet

	sort.Slice(s.payload.Addresses, func(i, j int) bool {
		if s.payload.Addresses[i].Branch == s.payload.Addresses[j].Branch {
			return s.payload.Addresses[i].Index < s.payload.Addresses[j].Index
		}
		return s.payload.Addresses[i].Branch < s.payload.Addresses[j].Branch
	})
	for _, rec := range s.payload.Addresses {
		if rec.Branch > 1 {
			return fmt.Errorf("invalid address branch %d", rec.Branch)
		}
		if _, err := wallet.DeriveAt(rec.Branch, rec.Index); err != nil {
			return fmt.Errorf("derive %d/%d: %w", rec.Branch, rec.Index, err)
		}
	}
	if len(s.payload.Addresses) == 0 {
		if _, err := s.addNewReceiveLocked(); err != nil {
			return err
		}
	}
	for _, u := range s.payload.UTXOs {
		script, err := hex.DecodeString(u.ScriptHex)
		if err != nil {
			return fmt.Errorf("decode utxo script %s:%d: %w", u.TxID, u.Vout, err)
		}
		if err := wallet.ForceImportUTXO(u.TxID, u.Vout, u.Value, script, u.Height); err != nil {
			return fmt.Errorf("import utxo %s:%d: %w", u.TxID, u.Vout, err)
		}
	}
	s.wireRuntimeLocked(node, wallet)
	if err := s.connectLocked(); err != nil {
		return err
	}
	s.startStatusLoopLocked()
	return s.saveLocked()
}

func (s *Service) addNewReceiveLocked() (string, error) {
	derived, err := s.wallet.NewDerivedAddress()
	if err != nil {
		return "", err
	}
	s.addAddressRecordLocked(store.AddressRecord{
		Branch:    derived.Branch,
		Index:     derived.Index,
		Address:   derived.Address,
		ScriptHex: hex.EncodeToString(derived.Script),
	})
	return derived.Address, nil
}

func (s *Service) addAddressRecordLocked(rec store.AddressRecord) {
	if rec.Address == "" && rec.ScriptHex == "" {
		return
	}
	for _, existing := range s.payload.Addresses {
		if existing.Branch == rec.Branch && existing.Index == rec.Index {
			return
		}
	}
	s.payload.Addresses = append(s.payload.Addresses, rec)
	next := rec.Index + 1
	if rec.Branch == 0 && next > s.payload.NextExternal {
		s.payload.NextExternal = next
	}
	if rec.Branch == 1 && next > s.payload.NextChange {
		s.payload.NextChange = next
	}
}

func (s *Service) refreshUTXOsLocked() {
	if s.wallet == nil {
		return
	}
	seen := make(map[string]time.Time, len(s.payload.UTXOs))
	for _, u := range s.payload.UTXOs {
		seen[utxoKey(u.TxID, u.Vout)] = u.SeenAt
	}
	now := time.Now().UTC()
	src := s.wallet.UTXOs()
	next := make([]store.UTXORecord, 0, len(src))
	for _, u := range src {
		at := seen[utxoKey(u.TxID, u.Vout)]
		if at.IsZero() {
			at = now
		}
		next = append(next, store.UTXORecord{
			TxID:      u.TxID,
			Vout:      u.Vout,
			Value:     u.Value,
			ScriptHex: hex.EncodeToString(u.Script),
			Height:    u.Height,
			SeenAt:    at,
		})
	}
	sort.Slice(next, func(i, j int) bool {
		if next[i].TxID == next[j].TxID {
			return next[i].Vout < next[j].Vout
		}
		return next[i].TxID < next[j].TxID
	})
	s.payload.UTXOs = next
}

func (s *Service) appendHistoryLocked(rec store.TxRecord) {
	if rec.SeenAt.IsZero() {
		rec.SeenAt = time.Now().UTC()
	}
	for i, existing := range s.payload.History {
		if existing.TxID == rec.TxID && existing.Direction == rec.Direction && existing.Vout == rec.Vout && existing.Address == rec.Address {
			s.payload.History[i] = rec
			return
		}
	}
	s.payload.History = append([]store.TxRecord{rec}, s.payload.History...)
	if len(s.payload.History) > 500 {
		s.payload.History = s.payload.History[:500]
	}
}

func (s *Service) wireRuntimeLocked(node *bsv.Node, wallet *bsv.Wallet) {
	node.OnPeerConnect(func(addr string) {
		s.mu.Lock()
		active := s.node == node
		if active {
			s.publishLocked(EventPeer, "peer connected: "+addr)
		}
		s.mu.Unlock()
	})
	node.OnPeerDisconnect(func(addr string, reason error) {
		s.mu.Lock()
		active := s.node == node
		if active {
			msg := "peer disconnected: " + addr
			if reason != nil {
				msg += " (" + reason.Error() + ")"
			}
			s.publishLocked(EventPeer, msg)
		}
		s.mu.Unlock()
	})
	wallet.OnPayment(func(p bsv.Payment) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.wallet != wallet || s.payload == nil {
			return
		}
		s.refreshUTXOsLocked()
		s.appendHistoryLocked(store.TxRecord{
			TxID:      p.TxID,
			Vout:      p.Vout,
			Direction: "in",
			Address:   p.Address,
			Amount:    p.Amount,
			Height:    -1,
			Status:    "seen",
			SeenAt:    time.Now().UTC(),
		})
		if err := s.saveLocked(); err != nil {
			s.publishLocked(EventError, err.Error())
			return
		}
		s.publishLocked(EventPayment, fmt.Sprintf("received %d sat at %s", p.Amount, p.Address))
	})
}

func (s *Service) connectLocked() error {
	if s.node == nil {
		return errors.New("node missing")
	}
	if err := s.node.Connect(); err != nil {
		s.publishLocked(EventError, err.Error())
		return err
	}
	s.publishLocked(EventStatus, "node started")
	return nil
}

func (s *Service) startStatusLoopLocked() {
	if s.statusStop != nil {
		close(s.statusStop)
	}
	stop := make(chan struct{})
	s.statusStop = stop
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				continue
			case <-stop:
				return
			}
		}
	}()
}

func (s *Service) stopRuntimeLocked(clearSecrets bool) {
	if s.statusStop != nil {
		close(s.statusStop)
		s.statusStop = nil
	}
	if s.node != nil {
		s.node.Disconnect()
	}
	s.node = nil
	s.wallet = nil
	if clearSecrets {
		s.passphrase = ""
		s.payload = nil
	}
}

func (s *Service) resetRuntimeFromPayloadLocked() {
	if s.payload == nil || s.passphrase == "" {
		return
	}
	payload := clonePayload(s.payload)
	passphrase := s.passphrase
	s.stopRuntimeLocked(false)
	if err := s.startExistingRuntimeLocked(payload, passphrase); err != nil {
		s.publishLocked(EventError, "reset wallet state: "+err.Error())
	}
}

func (s *Service) saveLocked() error {
	if s.payload == nil {
		return errors.New("payload missing")
	}
	if s.passphrase == "" {
		return errors.New("passphrase missing")
	}
	return store.Save(s.path, s.passphrase, s.payload)
}

func (s *Service) statusLocked() NodeStatus {
	st := NodeStatus{}
	if s.payload != nil {
		st.Network = s.payload.Network
	}
	if s.node == nil {
		return st
	}
	st.Unlocked = s.wallet != nil
	st.Connected = true
	st.PeerCount = s.node.PeerCount()
	st.BestPeerHeight = s.node.BestPeerHeight()
	st.ChainHeight = s.node.ChainHeight()
	return st
}

func (s *Service) publish(kind EventType, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(kind, message)
}

func (s *Service) publishLocked(kind EventType, message string) {
	ev := Event{Type: kind, Message: message, At: time.Now().UTC()}
	select {
	case s.events <- ev:
	default:
	}
}

func newNode(payload *store.Payload) *bsv.Node {
	cfg := bsv.DefaultConfig()
	cfg.MaxPeers = payload.MaxPeers
	cfg.FeePerByte = payload.FeePerByte
	cfg.UserAgent = "/ffswallet:0.1/"
	cfg.SkipBlockDownload = true
	return bsv.New(networkType(payload.Network), cfg)
}

func networkType(network string) bsv.NetworkType {
	switch NormalizeNetwork(network) {
	case NetworkMainnet:
		return bsv.Mainnet
	case NetworkSTN:
		return bsv.STN
	case NetworkRegtest:
		return bsv.Regtest
	default:
		return bsv.Testnet
	}
}

func NormalizeNetwork(network string) string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "main", "mainnet":
		return NetworkMainnet
	case "stn":
		return NetworkSTN
	case "regtest", "reg":
		return NetworkRegtest
	default:
		return NetworkTestnet
	}
}

func NetworkLabel(network string) string {
	switch NormalizeNetwork(network) {
	case NetworkMainnet:
		return "Mainnet"
	case NetworkSTN:
		return "STN"
	case NetworkRegtest:
		return "Regtest"
	default:
		return "Testnet"
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

func normalizeMnemonic(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func parseDisplayBlockHash(input string) ([32]byte, error) {
	var hash [32]byte
	input = strings.TrimSpace(input)
	if input == "" {
		return hash, errors.New("start block hash required")
	}
	raw, err := hex.DecodeString(input)
	if err != nil || len(raw) != 32 {
		return hash, errors.New("start block hash must be 64 hex characters")
	}
	for i := 0; i < 32; i++ {
		hash[i] = raw[31-i]
	}
	return hash, nil
}

func currentReceiveAddress(records []store.AddressRecord) string {
	var (
		addr string
		max  uint32
		seen bool
	)
	for _, rec := range records {
		if rec.Branch != 0 {
			continue
		}
		if !seen || rec.Index >= max {
			addr = rec.Address
			max = rec.Index
			seen = true
		}
	}
	return addr
}

func utxoKey(txid string, vout uint32) string {
	return fmt.Sprintf("%s:%d", txid, vout)
}

func clonePayload(p *store.Payload) *store.Payload {
	if p == nil {
		return nil
	}
	raw, _ := json.Marshal(p)
	var out store.Payload
	_ = json.Unmarshal(raw, &out)
	return &out
}
