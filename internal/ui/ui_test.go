package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/brad1121/FFSWallet/internal/app"
	"github.com/brad1121/FFSWallet/internal/store"
	"github.com/brad1121/FFSWallet/internal/update"
)

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name  string
		input string
		unit  string
		want  int64
	}{
		{name: "sats", input: "123", unit: "sats", want: 123},
		{name: "bsv whole", input: "1", unit: "BSV", want: 100000000},
		{name: "bsv decimal", input: "0.00000042", unit: "BSV", want: 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAmount(tc.input, tc.unit)
			if err != nil {
				t.Fatalf("parse amount: %v", err)
			}
			if got != tc.want {
				t.Fatalf("amount: got %d want %d", got, tc.want)
			}
		})
	}
}

func TestParseAmountRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct{ input, unit string }{
		{"", "sats"},
		{"0", "sats"},
		{"-1", "sats"},
		{"abc", "sats"},
		{"0.000000001", "BSV"},
		{"-0.1", "BSV"},
	} {
		if _, err := parseAmount(tc.input, tc.unit); err == nil {
			t.Fatalf("expected error for %#v", tc)
		}
	}
}

func TestParseRescanHeight(t *testing.T) {
	if got, err := parseRescanHeight(""); err != nil || got != 0 {
		t.Fatalf("blank height: got %d err=%v", got, err)
	}
	if got, err := parseRescanHeight("1713168"); err != nil || got != 1713168 {
		t.Fatalf("height: got %d err=%v", got, err)
	}
	for _, input := range []string{"-1", "abc"} {
		if _, err := parseRescanHeight(input); err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}

func TestBindDefaultRescanHeight(t *testing.T) {
	hashEntry := widget.NewEntry()
	heightEntry := widget.NewEntry()
	heightEntry.SetText("1713168")
	bindDefaultRescanHeight(hashEntry, heightEntry, app.NetworkTestnet)

	hashEntry.SetText("0000000000000000000000000000000000000000000000000000000000000000")
	if heightEntry.Text != "" {
		t.Fatalf("custom hash should clear default height, got %q", heightEntry.Text)
	}
	hashEntry.SetText(app.DefaultRescanStartHash(app.NetworkTestnet))
	if heightEntry.Text != "1713168" {
		t.Fatalf("default hash should restore default height, got %q", heightEntry.Text)
	}
}

func TestNetworkHelpers(t *testing.T) {
	if got := networkFromLabel("Mainnet"); got != app.NetworkMainnet {
		t.Fatalf("mainnet label: got %q", got)
	}
	if got := networkFromLabel("STN"); got != app.NetworkSTN {
		t.Fatalf("stn label: got %q", got)
	}
	if got := networkFromLabel("Regtest"); got != app.NetworkRegtest {
		t.Fatalf("regtest label: got %q", got)
	}
	if got := networkFromLabel("anything else"); got != app.NetworkTestnet {
		t.Fatalf("default label: got %q", got)
	}
	if labels := networkLabels(); len(labels) != 4 || labels[0] != "Testnet" {
		t.Fatalf("labels: got %#v", labels)
	}
}

func TestWhatsOnChainTxURL(t *testing.T) {
	tests := map[string]string{
		app.NetworkMainnet: "https://whatsonchain.com/tx/abc",
		app.NetworkTestnet: "https://test.whatsonchain.com/tx/abc",
		app.NetworkSTN:     "https://stn.whatsonchain.com/tx/abc",
	}
	for network, want := range tests {
		got := whatsOnChainTxURL(network, "abc")
		if got == nil || got.String() != want {
			t.Fatalf("url for %s: got %v want %s", network, got, want)
		}
	}
	if got := whatsOnChainTxURL(app.NetworkRegtest, "abc"); got != nil {
		t.Fatalf("regtest url: got %v want nil", got)
	}
}

func TestDetailJSONHelpers(t *testing.T) {
	now := time.Unix(10, 0).UTC()
	utxo := store.UTXORecord{TxID: "tx", Vout: 1, Value: 42, ScriptHex: "51", Height: 5, SeenAt: now}
	history := []store.TxRecord{{TxID: "tx", Vout: 1, Direction: "in", Amount: 42, SeenAt: now}}

	utxoJSON := utxoDetailJSON(utxo, history)
	if !json.Valid([]byte(utxoJSON)) || !strings.Contains(utxoJSON, `"value_sats": 42`) {
		t.Fatalf("utxo detail json: %s", utxoJSON)
	}

	tx := store.TxRecord{TxID: "tx", Direction: "out", Amount: -42, SeenAt: now}
	historyJSON := historyDetailJSON(tx, []store.UTXORecord{utxo})
	if !json.Valid([]byte(historyJSON)) || !strings.Contains(historyJSON, `"amount_bsv": "-0.00000042"`) {
		t.Fatalf("history detail json: %s", historyJSON)
	}
}

func TestFormatSatsAndShort(t *testing.T) {
	if got := formatSats(123456789); got != "1.23456789 BSV (123456789 sat)" {
		t.Fatalf("format sats: got %q", got)
	}
	if got := formatSats(-42); got != "-0.00000042 BSV (-42 sat)" {
		t.Fatalf("format negative sats: got %q", got)
	}
	if got := short("1234567890abcdefXYZ1234567890abc"); got != "12345678...67890abc" {
		t.Fatalf("short: got %q", got)
	}
	if got := short("short"); got != "short" {
		t.Fatalf("short passthrough: got %q", got)
	}
}

func TestFormatHeight(t *testing.T) {
	cases := []struct {
		chain, peer, synced int32
		rate                float64
		phase               string
		want                string
	}{
		{-1, -1, 0, 0, "", "Height: — · not scanned"},
		{850000, 850000, 0, 0, "", "Height: 850000 · not scanned"},
		{850000, 850000, 850000, 0, "", "Height: 850000 · scanned to 850000"},
		{850000, 850002, 850000, 0, "", "Height: 850000 / 850002 (syncing) · scanned to 850000 (2 behind)"},
		{850000, -1, 850000, 0, "", "Height: 850000 · scanned to 850000"},
		// A wallet reopened after a week: the node knows the tip from headers
		// long before the scan has replayed the blocks up to it.
		{849000, 850000, 849500, 0, "", "Height: 849000 / 850000 (syncing) · scanned to 849500 (500 behind)"},
		// While a scan runs, the rate and what it implies for the remaining
		// blocks is the part worth reading.
		{849000, 850000, 849500, 33.2, "", "Height: 849000 / 850000 (syncing) · scanned to 849500 (500 behind, 33 blk/s, <1m left)"},
		{849000, 850000, 840000, 4.5, "", "Height: 849000 / 850000 (syncing) · scanned to 840000 (10000 behind, 4.5 blk/s, ~37m left)"},
		{849000, 850000, 800000, 4.5, "", "Height: 849000 / 850000 (syncing) · scanned to 800000 (50000 behind, 4.5 blk/s, ~3h05m left)"},
		// Caught up but still scanning: rate without an estimate.
		{850000, 850000, 850000, 12, "", "Height: 850000 · scanned to 850000, 12 blk/s"},
		// A scan that has not replayed a block yet says what it is doing
		// rather than showing nothing, which reads as "not scanning".
		{849000, 850000, 849500, 0, "waiting for peers", "Height: 849000 / 850000 (syncing) · scanned to 849500 (500 behind, waiting for peers)"},
		{849000, 850000, 849500, 0, "fetching headers", "Height: 849000 / 850000 (syncing) · scanned to 849500 (500 behind, fetching headers)"},
		// A stalled scan must not keep showing the last speed as if it were
		// still moving: the phase wins.
		{849000, 850000, 849500, 33.2, "waiting for a block", "Height: 849000 / 850000 (syncing) · scanned to 849500 (500 behind, waiting for a block)"},
	}
	for _, c := range cases {
		if got := formatHeight(c.chain, c.peer, c.synced, c.rate, c.phase); got != c.want {
			t.Fatalf("formatHeight(%d,%d,%d,%v,%q): got %q want %q", c.chain, c.peer, c.synced, c.rate, c.phase, got, c.want)
		}
	}
}

func TestFormatETABuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m left"},
		{90 * time.Second, "~1m left"},
		{45 * time.Minute, "~45m left"},
		{90 * time.Minute, "~1h30m left"},
		{26 * time.Hour, "~26h00m left"},
		{72 * time.Hour, "~3d left"},
	}
	for _, c := range cases {
		if got := formatETA(c.d); got != c.want {
			t.Fatalf("formatETA(%s): got %q want %q", c.d, got, c.want)
		}
	}
}

// TestMainLayoutFitsInitialWindow guards the window against asking to grow.
// Sway re-centres a floating window on every size request a client makes, and
// Fyne makes one whenever the content's minimum size exceeds the window. A
// settings tab taller than the window, or a status line longer than it is
// wide, therefore snaps a window the user has just moved back to the centre
// of the screen. Every screen, with long runtime text in place, must fit.
func TestMainLayoutFitsInitialWindow(t *testing.T) {
	a := test.NewTempApp(t)
	svc := app.NewService(t.TempDir())
	u := newUI(svc, a)
	defer u.win.Close()

	check := func(screen string) {
		t.Helper()
		min := u.screen().MinSize()
		if min.Width > initialWindowSize.Width || min.Height > initialWindowSize.Height {
			t.Fatalf("%s: min size %.0fx%.0f exceeds the %.0fx%.0f window — the compositor would be asked to resize",
				screen, min.Width, min.Height, initialWindowSize.Width, initialWindowSize.Height)
		}
	}

	u.showHome()
	check("home")
	u.showUnlock()
	check("unlock")
	u.showRescanChoice(app.NetworkTestnet)
	check("rescan choice")
	u.showMnemonic(strings.Repeat("abandon ", 23) + "about")
	check("mnemonic")

	u.showMain()
	// The text these labels carry at runtime, at its longest.
	u.heightLabel.SetText(formatHeight(1757227, 1757227, 1715168, 0.4, "waiting for a block from the peer"))
	u.balanceLabel.SetText("Balance: 21,000,000.00000000 BSV")
	u.statusLabel.SetText("Wallet: " + strings.Repeat("w", 40) + " | Fee: 1000 sat/byte | Store: /" + strings.Repeat("directory/", 12) + "wallet.json")
	u.addressLabel.SetText(strings.Repeat("m", 64))
	for i := 0; i < 200; i++ {
		u.addEvent(app.Event{Type: app.EventStatus, Message: strings.Repeat("rescan block peer=1.2.3.4:18333 ", 6)})
	}
	check("main")
}

// TestRootMinSizeNeverChanges: the window's root reports one minimum size no
// matter what the screen inside it asks for. Fyne re-requests the window size
// from the compositor whenever the root's minimum changes, and under Sway that
// re-centres a floating window.
func TestRootMinSizeNeverChanges(t *testing.T) {
	a := test.NewTempApp(t)
	u := newUI(app.NewService(t.TempDir()), a)
	defer u.win.Close()

	before := u.win.Content().MinSize()
	if before != rootMinSize {
		t.Fatalf("root min %v, want %v", before, rootMinSize)
	}
	u.showHome()
	u.showMain()
	u.heightLabel.SetText(strings.Repeat("Height: 1757291 · scanned to 1719168 (38123 behind) ", 4))
	tall := widget.NewLabel(strings.Repeat("row\n", 200))
	u.setContent(tall)
	if tall.MinSize().Height <= rootMinSize.Height {
		t.Fatal("test screen is not taller than the root minimum; the test proves nothing")
	}
	if after := u.win.Content().MinSize(); after != before {
		t.Fatalf("root min changed from %v to %v — the window would ask the compositor to resize", before, after)
	}
}

// TestUpdateBannerShowsOnEveryScreen: the notice sits above whichever screen
// is current, hidden until a newer release is known, and survives a screen
// change.
func TestUpdateBannerShowsOnEveryScreen(t *testing.T) {
	a := test.NewTempApp(t)
	u := newUI(app.NewService(t.TempDir()), a)
	defer u.win.Close()

	u.showHome()
	if u.updateBanner.Visible() {
		t.Fatal("banner visible before any check")
	}
	u.showUpdateBanner("0.0.8", update.Latest{Tag: "v0.0.9", Version: "0.0.9", URL: "https://example.test/v0.0.9"})
	if !u.updateBanner.Visible() {
		t.Fatal("banner hidden after an outdated result")
	}
	if !strings.Contains(u.updateLink.Text, "v0.0.9") || !strings.Contains(u.updateLink.Text, "0.0.8") {
		t.Fatalf("banner text %q names neither version", u.updateLink.Text)
	}
	if u.updateLink.URL == nil || u.updateLink.URL.String() != "https://example.test/v0.0.9" {
		t.Fatalf("banner link %v, want the release page", u.updateLink.URL)
	}
	u.showMain()
	if !u.updateBanner.Visible() {
		t.Fatal("banner lost on screen change")
	}
	if min := u.win.Content().MinSize(); min != rootMinSize {
		t.Fatalf("root min %v changed with the banner shown", min)
	}
}
