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

	"github.com/brad1121/FFSWallet/internal/bsvsdk"
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
	EventReject  EventType = "reject"
	EventTraffic EventType = "traffic"
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
	SyncedHeight   int32
	Status         NodeStatus
}

type Service struct {
	mu     sync.RWMutex
	sendMu sync.Mutex
	scanMu sync.Mutex

	baseDir    string
	walletName string
	path       string
	passphrase string
	payload    *store.Payload
	runtime    *bsvsdk.Runtime
	node       *bsvsdk.Node
	wallet     *bsvsdk.Wallet

	events        chan Event
	statusStop    chan struct{}
	p2pTraffic    bool
	pendingSeeded bool
}

func NewService(baseDir string) *Service {
	return &Service{
		baseDir: baseDir,
		events:  make(chan Event, 4096),
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

func (s *Service) SetP2PTrafficEnabled(enabled bool) {
	s.mu.Lock()
	changed := s.p2pTraffic != enabled
	s.p2pTraffic = enabled
	if changed {
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		s.publishLocked(EventStatus, "p2p traffic logging "+state)
	}
	s.mu.Unlock()
}

func (s *Service) P2PTrafficEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.p2pTraffic
}

func (s *Service) WalletPath() string {
	return s.path
}

func (s *Service) WalletName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.walletName
}

func (s *Service) WalletNames() []string {
	names, _ := store.ListWalletNames(s.baseDir)
	return names
}

func (s *Service) HasWallets() bool {
	return len(s.WalletNames()) > 0
}

func (s *Service) SelectWallet(name string) error {
	name, err := store.NormalizeWalletName(name)
	if err != nil {
		return err
	}
	path := store.WalletPath(s.baseDir, name)
	if !store.Exists(path) {
		return fmt.Errorf("wallet %q not found", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(true)
	s.walletName = name
	s.path = path
	return nil
}

func (s *Service) WalletExists() bool {
	return store.Exists(s.path)
}

func (s *Service) CreateWallet(name, passphrase, network string) (string, error) {
	if strings.TrimSpace(passphrase) == "" {
		return "", errors.New("passphrase required")
	}
	name, path, err := s.newWalletTarget(name)
	if err != nil {
		return "", err
	}
	network = NormalizeNetwork(network)
	payload := store.DefaultPayload(network)
	payload.WalletName = name

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(false)

	runtime, mnemonic, err := bsvsdk.CreateWallet(context.Background(), s.runtimeConfig(payload))
	if err != nil {
		return "", err
	}
	node := runtime.Node
	wallet := runtime.Wallet
	payload.Mnemonic = mnemonic

	s.walletName = name
	s.path = path
	s.passphrase = passphrase
	s.payload = payload
	s.runtime = runtime
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
	if err := runtime.SaveAddressSnapshot(); err != nil {
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

func (s *Service) RestoreWallet(name, mnemonic, passphrase, network string) error {
	return s.createWalletFromMnemonic(name, mnemonic, passphrase, network, "wallet restored")
}

func (s *Service) CreateWalletFromSeed(name, seedWords, passphrase, network string) error {
	return s.createWalletFromMnemonic(name, seedWords, passphrase, network, "wallet created from seed")
}

func (s *Service) createWalletFromMnemonic(name, mnemonic, passphrase, network, eventMessage string) error {
	if strings.TrimSpace(passphrase) == "" {
		return errors.New("passphrase required")
	}
	name, path, err := s.newWalletTarget(name)
	if err != nil {
		return err
	}
	mnemonic = normalizeMnemonic(mnemonic)
	if mnemonic == "" {
		return errors.New("mnemonic required")
	}
	network = NormalizeNetwork(network)
	payload := store.DefaultPayload(network)
	payload.WalletName = name
	payload.Mnemonic = mnemonic

	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(false)

	runtime, err := bsvsdk.RestoreWallet(context.Background(), s.runtimeConfig(payload))
	if err != nil {
		return err
	}
	node := runtime.Node
	wallet := runtime.Wallet
	s.walletName = name
	s.path = path
	s.passphrase = passphrase
	s.payload = payload
	s.runtime = runtime
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
	if err := runtime.SaveAddressSnapshot(); err != nil {
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
	if s.path == "" {
		return errors.New("select wallet first")
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
	// Blocks mined while this wallet was closed were never seen: the node only
	// follows the mempool, so those payments are missing from the balance
	// until their blocks are replayed. Catch up in the background so the
	// window stays usable while it runs.
	go s.runCatchUp()
	return nil
}

// runCatchUp drives CatchUp and reports through the event log; the rescan
// itself publishes its own progress and completion.
func (s *Service) runCatchUp() {
	msg, err := s.CatchUp()
	if err != nil {
		s.publish(EventError, "catch-up: "+err.Error())
		return
	}
	if msg == "" {
		s.publish(EventStatus, "no sync cursor yet; run a rescan to establish one")
	}
}

func (s *Service) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRuntimeLocked(true)
	s.publishLocked(EventWallet, "wallet locked")
}

func (s *Service) AddPeer(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errors.New("peer address required")
	}

	s.mu.RLock()
	node := s.node
	s.mu.RUnlock()
	if node == nil {
		return errors.New("wallet locked")
	}
	if err := node.ConnectPeer(addr); err != nil {
		return err
	}
	s.publish(EventPeer, "manual peer dial: "+addr)
	return nil
}

func (s *Service) SetFeePerByte(fee int64) error {
	if fee <= 0 {
		return errors.New("fee rate must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payload == nil {
		return errors.New("wallet locked")
	}
	s.payload.FeePerByte = fee
	if err := s.saveLocked(); err != nil {
		return err
	}
	if s.wallet != nil {
		s.resetRuntimeFromPayloadLocked()
	}
	s.publishLocked(EventStatus, fmt.Sprintf("fee rate set to %d sat/byte", fee))
	return nil
}

func (s *Service) newWalletTarget(name string) (string, string, error) {
	name, err := store.NormalizeWalletName(name)
	if err != nil {
		return "", "", err
	}
	path := store.WalletPath(s.baseDir, name)
	if store.Exists(path) {
		return "", "", fmt.Errorf("wallet %q already exists", name)
	}
	return name, path, nil
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
	if s.runtime != nil {
		if err := s.runtime.SaveAddressSnapshot(); err != nil {
			return "", err
		}
	}
	s.publishLocked(EventWallet, "receive address generated")
	return addr, nil
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
	snap.SyncedHeight = s.payload.SyncedHeight
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
	runtime, err := bsvsdk.RestoreWallet(context.Background(), s.runtimeConfig(payload))
	if err != nil {
		return err
	}
	node := runtime.Node
	wallet := runtime.Wallet

	s.passphrase = passphrase
	s.payload = payload
	s.runtime = runtime
	s.node = node
	s.wallet = wallet

	snapshotLoaded, err := runtime.LoadAddressSnapshot()
	if err != nil {
		return fmt.Errorf("load address snapshot: %w", err)
	}
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
		if snapshotLoaded && rec.Index < wallet.NextIndex(rec.Branch) {
			continue
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
	loaded, err := runtime.LoadStore(context.Background())
	if err != nil {
		return err
	}
	if loaded == 0 && len(s.payload.UTXOs) > 0 {
		for _, u := range s.payload.UTXOs {
			script, err := hex.DecodeString(u.ScriptHex)
			if err != nil {
				return fmt.Errorf("decode legacy utxo script %s:%d: %w", u.TxID, u.Vout, err)
			}
			if err := wallet.ForceImportUTXO(u.TxID, u.Vout, u.Value, script, u.Height); err != nil {
				return fmt.Errorf("import legacy utxo %s:%d: %w", u.TxID, u.Vout, err)
			}
		}
		s.publishLocked(EventStatus, "loaded legacy JSON UTXO cache; run rebuild rescan to seed SDK store")
	}
	s.refreshUTXOsLocked()
	if err := runtime.SaveAddressSnapshot(); err != nil {
		return err
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
	s.applyUTXOSnapshotLocked(s.wallet.UTXOs())
}

func (s *Service) applyUTXOSnapshotLocked(src []bsvsdk.UTXO) {
	seen := make(map[string]time.Time, len(s.payload.UTXOs))
	for _, u := range s.payload.UTXOs {
		seen[utxoKey(u.TxID, u.Vout)] = u.SeenAt
	}
	now := time.Now().UTC()
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

func (s *Service) wireRuntimeLocked(node *bsvsdk.Node, wallet *bsvsdk.Wallet) {
	node.OnPeerConnect(func(addr string) {
		s.mu.Lock()
		active := s.node == node
		if active {
			s.publishLocked(EventPeer, "peer connected: "+addr)
		}
		s.mu.Unlock()
		if active {
			s.seedPendingFromHistory(node)
		}
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
	node.OnReject(func(reject bsvsdk.Reject) {
		s.mu.Lock()
		active := s.node == node
		if active {
			s.publishLocked(EventReject, formatReject(reject))
		}
		s.mu.Unlock()
	})
	node.OnP2PTraffic(func(traffic bsvsdk.P2PTraffic) {
		s.mu.RLock()
		active := s.node == node && s.p2pTraffic
		s.mu.RUnlock()
		if !active {
			return
		}
		s.publish(EventTraffic, formatP2PTraffic(traffic))
	})
	wallet.OnWalletTx(func(tx *bsvsdk.Transaction, owned []bsvsdk.OwnedOutput, spent []bsvsdk.SpentOutPoint) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.wallet != wallet || s.payload == nil {
			return
		}
		txID := tx.TxID()
		changed := false
		if len(spent) > 0 {
			if s.markOutgoingRelayedLocked(txID) {
				changed = true
			}
			s.refreshUTXOsLocked()
			if err := s.saveLocked(); err != nil {
				s.publishLocked(EventError, err.Error())
			}
			return
		}
		for _, o := range owned {
			alreadyKnown := false
			for _, existing := range s.payload.UTXOs {
				if existing.TxID == txID && existing.Vout == o.Vout {
					alreadyKnown = true
					break
				}
			}
			if alreadyKnown {
				continue
			}
			s.appendHistoryLocked(store.TxRecord{
				TxID:      txID,
				Vout:      o.Vout,
				Direction: "in",
				Address:   o.Address,
				Amount:    o.Value,
				Height:    -1,
				Status:    "seen",
				SeenAt:    time.Now().UTC(),
			})
			s.publishLocked(EventPayment, fmt.Sprintf("received tx=%s vout=%d amount=%d sat at=%s", txID, o.Vout, o.Value, o.Address))
			changed = true
		}
		if !changed {
			return
		}
		s.refreshUTXOsLocked()
		if err := s.saveLocked(); err != nil {
			s.publishLocked(EventError, err.Error())
		}
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
	if s.runtime != nil {
		s.runtime.Close()
	} else if s.node != nil {
		s.node.Disconnect()
	}
	s.runtime = nil
	s.node = nil
	s.wallet = nil
	s.pendingSeeded = false
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

func (s *Service) runtimeConfig(payload *store.Payload) bsvsdk.RuntimeConfig {
	return bsvsdk.RuntimeConfig{
		WalletName:   payload.WalletName,
		Mnemonic:     payload.Mnemonic,
		Network:      payload.Network,
		FeePerByte:   payload.FeePerByte,
		MaxPeers:     payload.MaxPeers,
		StorePath:    store.WalletStorePath(s.baseDir, payload.WalletName),
		SnapshotPath: store.WalletSnapshotPath(s.baseDir, payload.WalletName),
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

func normalizeMnemonic(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func shortTxID(txid string) string {
	if len(txid) <= 16 {
		return txid
	}
	return txid[:8] + "..." + txid[len(txid)-8:]
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

func formatReject(reject bsvsdk.Reject) string {
	reason := strings.TrimSpace(reject.Reason)
	if reason == "" {
		reason = "no reason"
	}
	target := ""
	if reject.Hash != "" {
		label := "hash"
		if reject.Command == "tx" {
			label = "tx"
		}
		target = fmt.Sprintf(" %s=%s", label, reject.Hash)
	}
	return fmt.Sprintf("peer reject peer=%s command=%s%s code=0x%02x/%s reason=%q", reject.Peer, reject.Command, target, reject.Code, reject.CodeName, reason)
}

func formatP2PTraffic(traffic bsvsdk.P2PTraffic) string {
	msg := fmt.Sprintf("p2p %s peer=%s cmd=%s bytes=%d", traffic.Direction, traffic.Peer, traffic.Command, traffic.PayloadBytes)
	if strings.TrimSpace(traffic.Summary) != "" {
		msg += " " + traffic.Summary
	}
	return msg
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
