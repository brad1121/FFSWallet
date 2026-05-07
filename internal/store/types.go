package store

import "time"

const (
	FileVersion    = 1
	PayloadVersion = 1
)

type KDF struct {
	Algorithm string `json:"algorithm"`
	Time      uint32 `json:"time"`
	MemoryKB  uint32 `json:"memory_kb"`
	Threads   uint8  `json:"threads"`
	KeyBytes  uint32 `json:"key_bytes"`
}

type EncryptedFile struct {
	Version    int    `json:"version"`
	KDF        KDF    `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Payload struct {
	Version      int             `json:"version"`
	WalletName   string          `json:"wallet_name"`
	Network      string          `json:"network"`
	Mnemonic     string          `json:"mnemonic"`
	FeePerByte   int64           `json:"fee_per_byte"`
	MaxPeers     int             `json:"max_peers"`
	NextExternal uint32          `json:"next_external"`
	NextChange   uint32          `json:"next_change"`
	Addresses    []AddressRecord `json:"addresses"`
	UTXOs        []UTXORecord    `json:"utxos"`
	History      []TxRecord      `json:"history"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type AddressRecord struct {
	Branch    uint32 `json:"branch"`
	Index     uint32 `json:"index"`
	Address   string `json:"address"`
	ScriptHex string `json:"script_hex"`
}

type UTXORecord struct {
	TxID      string    `json:"txid"`
	Vout      uint32    `json:"vout"`
	Value     int64     `json:"value"`
	ScriptHex string    `json:"script_hex"`
	Height    int32     `json:"height"`
	SeenAt    time.Time `json:"seen_at"`
}

type TxRecord struct {
	TxID      string    `json:"txid"`
	Vout      uint32    `json:"vout,omitempty"`
	Direction string    `json:"direction"`
	Address   string    `json:"address"`
	Amount    int64     `json:"amount"`
	Height    int32     `json:"height"`
	Status    string    `json:"status"`
	SeenAt    time.Time `json:"seen_at"`
	Note      string    `json:"note,omitempty"`
}

func DefaultPayload(network string) *Payload {
	now := time.Now().UTC()
	return &Payload{
		Version:    PayloadVersion,
		WalletName: "default",
		Network:    network,
		FeePerByte: 1,
		MaxPeers:   16,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func (p *Payload) EnsureDefaults() {
	if p.Version == 0 {
		p.Version = PayloadVersion
	}
	if p.WalletName == "" {
		p.WalletName = "default"
	}
	if p.Network == "" {
		p.Network = "test"
	}
	if p.FeePerByte <= 0 {
		p.FeePerByte = 1
	}
	if p.MaxPeers <= 0 {
		p.MaxPeers = 16
	}
	for _, a := range p.Addresses {
		next := a.Index + 1
		if a.Branch == 0 && next > p.NextExternal {
			p.NextExternal = next
		}
		if a.Branch == 1 && next > p.NextChange {
			p.NextChange = next
		}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
}
