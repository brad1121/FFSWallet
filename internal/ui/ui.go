package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	walletapp "github.com/brad1121/FFSWallet/internal/app"
	"github.com/brad1121/FFSWallet/internal/store"
)

type UI struct {
	svc *walletapp.Service
	app fyne.App
	win fyne.Window

	balanceLabel *widget.Label
	networkLabel *widget.Label
	peerLabel    *widget.Label
	heightLabel  *widget.Label
	addressLabel *widget.Label
	statusLabel  *widget.Label

	addressBox *fyne.Container
	utxoBox    *fyne.Container
	historyBox *fyne.Container
	eventEntry *widget.Entry
	events     []string
	closed     chan struct{}
}

func Run(svc *walletapp.Service) {
	a := fyneapp.NewWithID("com.ffswallet.desktop")
	u := &UI{
		svc:    svc,
		app:    a,
		closed: make(chan struct{}),
	}
	u.win = a.NewWindow("FFSWallet")
	u.win.Resize(fyne.NewSize(1040, 700))
	u.win.SetCloseIntercept(func() {
		select {
		case <-u.closed:
		default:
			close(u.closed)
		}
		u.svc.Close()
		u.win.Close()
	})

	if svc.WalletExists() {
		u.showUnlock()
	} else {
		u.showSetup()
	}
	u.startLoops()
	u.win.ShowAndRun()
}

func (u *UI) startLoops() {
	go func() {
		for {
			select {
			case ev := <-u.svc.Events():
				fyne.Do(func() {
					u.addEvent(ev)
					u.refresh()
				})
			case <-u.closed:
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fyne.Do(u.refresh)
			case <-u.closed:
				return
			}
		}
	}()
}

func (u *UI) showSetup() {
	title := widget.NewLabelWithStyle("FFSWallet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	sub := widget.NewLabelWithStyle("Bitcoin SV desktop wallet", fyne.TextAlignCenter, fyne.TextStyle{})

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Create", theme.ContentAddIcon(), u.createWalletTab()),
		container.NewTabItemWithIcon("Seed Words", theme.HistoryIcon(), u.seedWalletTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	u.win.SetContent(container.NewBorder(
		container.NewVBox(title, sub, widget.NewSeparator()),
		nil,
		nil,
		nil,
		container.NewPadded(tabs),
	))
}

func (u *UI) createWalletTab() fyne.CanvasObject {
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("Passphrase")
	confirm := widget.NewPasswordEntry()
	confirm.SetPlaceHolder("Confirm passphrase")
	network := widget.NewSelect(networkLabels(), nil)
	network.SetSelected("Testnet")
	result := widget.NewLabel("")
	create := widget.NewButtonWithIcon("Create wallet", theme.ConfirmIcon(), nil)

	create.OnTapped = func() {
		if pass.Text == "" {
			dialog.ShowError(fmt.Errorf("passphrase required"), u.win)
			return
		}
		if pass.Text != confirm.Text {
			dialog.ShowError(fmt.Errorf("passphrases do not match"), u.win)
			return
		}
		run := func() {
			create.Disable()
			result.SetText("Creating wallet...")
			go func() {
				mnemonic, err := u.svc.CreateWallet(pass.Text, networkFromLabel(network.Selected))
				fyne.Do(func() {
					create.Enable()
					result.SetText("")
					if err != nil {
						dialog.ShowError(err, u.win)
						return
					}
					u.showMnemonic(mnemonic)
				})
			}()
		}
		if network.Selected == "Mainnet" {
			dialog.ShowConfirm("Mainnet", "Mainnet uses real BSV. Create mainnet wallet?", func(ok bool) {
				if ok {
					run()
				}
			}, u.win)
			return
		}
		run()
	}

	form := widget.NewForm(
		widget.NewFormItem("Network", network),
		widget.NewFormItem("Passphrase", pass),
		widget.NewFormItem("Confirm", confirm),
	)
	return container.NewVBox(form, create, result)
}

func (u *UI) seedWalletTab() fyne.CanvasObject {
	mnemonic := widget.NewMultiLineEntry()
	mnemonic.SetPlaceHolder("12 or 24 seed words")
	mnemonic.SetMinRowsVisible(4)
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("Passphrase")
	confirm := widget.NewPasswordEntry()
	confirm.SetPlaceHolder("Confirm passphrase")
	network := widget.NewSelect(networkLabels(), nil)
	network.SetSelected("Testnet")
	result := widget.NewLabel("")
	create := widget.NewButtonWithIcon("Create from seed", theme.ConfirmIcon(), nil)

	create.OnTapped = func() {
		if strings.TrimSpace(mnemonic.Text) == "" {
			dialog.ShowError(fmt.Errorf("seed words required"), u.win)
			return
		}
		if pass.Text == "" {
			dialog.ShowError(fmt.Errorf("passphrase required"), u.win)
			return
		}
		if pass.Text != confirm.Text {
			dialog.ShowError(fmt.Errorf("passphrases do not match"), u.win)
			return
		}
		run := func() {
			create.Disable()
			result.SetText("Creating wallet from seed...")
			go func() {
				networkName := networkFromLabel(network.Selected)
				err := u.svc.CreateWalletFromSeed(mnemonic.Text, pass.Text, networkName)
				fyne.Do(func() {
					create.Enable()
					result.SetText("")
					if err != nil {
						dialog.ShowError(err, u.win)
						return
					}
					u.showRescanChoice(networkName)
				})
			}()
		}
		if network.Selected == "Mainnet" {
			dialog.ShowConfirm("Mainnet", "Mainnet uses real BSV. Create mainnet wallet from seed?", func(ok bool) {
				if ok {
					run()
				}
			}, u.win)
			return
		}
		run()
	}

	form := widget.NewForm(
		widget.NewFormItem("Network", network),
		widget.NewFormItem("Seed words", mnemonic),
		widget.NewFormItem("Passphrase", pass),
		widget.NewFormItem("Confirm", confirm),
	)
	return container.NewVBox(form, create, result)
}

func (u *UI) showRescanChoice(network string) {
	startHash := widget.NewEntry()
	startHash.SetPlaceHolder("Start block hash")
	startHash.SetText(walletapp.DefaultRescanStartHash(network))
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	warning := widget.NewLabel("Rescan downloads and scans every block from the start hash. It can take hours. Skip if seed words have no previous activity.")
	warning.Wrapping = fyne.TextWrapWord
	defaultLabel := widget.NewLabel(walletapp.DefaultRescanStartLabel(network))
	defaultLabel.Wrapping = fyne.TextWrapWord

	skip := widget.NewButtonWithIcon("Skip rescan", theme.NavigateNextIcon(), func() {
		u.showMain()
	})
	rescan := widget.NewButtonWithIcon("Start rescan", theme.ViewRefreshIcon(), nil)
	rescan.OnTapped = func() {
		rescan.Disable()
		skip.Disable()
		status.SetText("Rescanning wallet. Keep app open.")
		go func() {
			msg, err := u.svc.RescanFromBlockHash(startHash.Text)
			fyne.Do(func() {
				rescan.Enable()
				skip.Enable()
				if err != nil {
					status.SetText("")
					dialog.ShowError(err, u.win)
					return
				}
				status.SetText(msg)
				u.showMain()
			})
		}()
	}

	u.win.SetContent(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("Seed wallet created", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		warning,
		defaultLabel,
		widget.NewForm(widget.NewFormItem("Start hash", startHash)),
		container.NewHBox(rescan, skip),
		status,
	)))
}

func (u *UI) showMnemonic(mnemonic string) {
	seed := widget.NewMultiLineEntry()
	seed.SetText(mnemonic)
	seed.SetMinRowsVisible(4)
	copyButton := widget.NewButtonWithIcon("Copy seed", theme.ContentCopyIcon(), func() {
		u.win.Clipboard().SetContent(mnemonic)
	})
	continueButton := widget.NewButtonWithIcon("I saved seed", theme.ConfirmIcon(), func() {
		u.showMain()
	})
	u.win.SetContent(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("Save mnemonic", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Seed is only shown now. Store offline before using wallet."),
		seed,
		container.NewHBox(copyButton, continueButton),
	)))
}

func (u *UI) showUnlock() {
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("Passphrase")
	status := widget.NewLabel("")
	unlock := widget.NewButtonWithIcon("Unlock", theme.LoginIcon(), nil)
	unlock.OnTapped = func() {
		if pass.Text == "" {
			dialog.ShowError(fmt.Errorf("passphrase required"), u.win)
			return
		}
		unlock.Disable()
		status.SetText("Unlocking...")
		go func() {
			err := u.svc.Unlock(pass.Text)
			fyne.Do(func() {
				unlock.Enable()
				status.SetText("")
				if err != nil {
					dialog.ShowError(err, u.win)
					return
				}
				u.showMain()
			})
		}()
	}
	u.win.SetContent(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("FFSWallet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Unlock wallet", fyne.TextAlignCenter, fyne.TextStyle{}),
		pass,
		unlock,
		status,
		widget.NewSeparator(),
		widget.NewLabel(u.svc.WalletPath()),
	)))
}

func (u *UI) showMain() {
	u.balanceLabel = widget.NewLabel("")
	u.networkLabel = widget.NewLabel("")
	u.peerLabel = widget.NewLabel("")
	u.heightLabel = widget.NewLabel("")
	u.addressLabel = widget.NewLabel("")
	u.addressLabel.Wrapping = fyne.TextWrapBreak
	u.statusLabel = widget.NewLabel("")
	u.addressBox = container.NewVBox()
	u.utxoBox = container.NewVBox()
	u.historyBox = container.NewVBox()
	u.eventEntry = widget.NewMultiLineEntry()
	u.eventEntry.SetMinRowsVisible(8)
	u.eventEntry.Disable()

	top := container.NewGridWithColumns(4,
		u.balanceLabel,
		u.networkLabel,
		u.peerLabel,
		u.heightLabel,
	)
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Dashboard", theme.HomeIcon(), u.dashboardTab()),
		container.NewTabItemWithIcon("Receive", theme.ContentAddIcon(), u.receiveTab()),
		container.NewTabItemWithIcon("Send", theme.MailSendIcon(), u.sendTab()),
		container.NewTabItemWithIcon("UTXOs", theme.StorageIcon(), u.utxosTab()),
		container.NewTabItemWithIcon("History", theme.HistoryIcon(), u.historyTab()),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), u.settingsTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	u.win.SetContent(container.NewBorder(top, u.statusLabel, nil, nil, tabs))
	u.refresh()
}

func (u *UI) dashboardTab() fyne.CanvasObject {
	newAddr := widget.NewButtonWithIcon("New receive", theme.ContentAddIcon(), func() {
		addr, err := u.svc.NewReceiveAddress()
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.win.Clipboard().SetContent(addr)
		u.refresh()
	})
	copyAddr := widget.NewButtonWithIcon("Copy address", theme.ContentCopyIcon(), func() {
		snap := u.svc.Snapshot()
		if snap.ReceiveAddress != "" {
			u.win.Clipboard().SetContent(snap.ReceiveAddress)
		}
	})
	return container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("Current receive address", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			u.addressLabel,
			container.NewHBox(newAddr, copyAddr),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Events", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		),
		nil,
		nil,
		nil,
		u.eventEntry,
	)
}

func (u *UI) receiveTab() fyne.CanvasObject {
	newAddr := widget.NewButtonWithIcon("New receive", theme.ContentAddIcon(), func() {
		addr, err := u.svc.NewReceiveAddress()
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.win.Clipboard().SetContent(addr)
		u.refresh()
	})
	return container.NewBorder(
		container.NewVBox(newAddr, widget.NewSeparator()),
		nil,
		nil,
		nil,
		container.NewVScroll(u.addressBox),
	)
}

func (u *UI) sendTab() fyne.CanvasObject {
	to := widget.NewEntry()
	to.SetPlaceHolder("Destination address")
	amount := widget.NewEntry()
	amount.SetPlaceHolder("Amount")
	unit := widget.NewSelect([]string{"sats", "BSV"}, nil)
	unit.SetSelected("sats")
	result := widget.NewLabel("")

	var send *widget.Button
	send = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), func() {
		sats, err := parseAmount(amount.Text, unit.Selected)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		dest := strings.TrimSpace(to.Text)
		if dest == "" {
			dialog.ShowError(fmt.Errorf("destination address required"), u.win)
			return
		}
		msg := fmt.Sprintf("Send %s to %s?", formatSats(sats), dest)
		dialog.ShowConfirm("Confirm send", msg, func(ok bool) {
			if !ok {
				return
			}
			send.Disable()
			result.SetText("Broadcasting...")
			go func() {
				txid, err := u.svc.Send(dest, sats)
				fyne.Do(func() {
					send.Enable()
					if err != nil {
						result.SetText("")
						dialog.ShowError(err, u.win)
						return
					}
					result.SetText("Broadcast: " + txid)
					amount.SetText("")
					to.SetText("")
					u.refresh()
				})
			}()
		}, u.win)
	})

	var sendAll *widget.Button
	sendAll = widget.NewButtonWithIcon("Send all", theme.UploadIcon(), func() {
		dest := strings.TrimSpace(to.Text)
		if dest == "" {
			dialog.ShowError(fmt.Errorf("destination address required"), u.win)
			return
		}
		dialog.ShowConfirm("Confirm sweep", "Send full wallet balance to destination?", func(ok bool) {
			if !ok {
				return
			}
			sendAll.Disable()
			result.SetText("Broadcasting sweep...")
			go func() {
				txid, err := u.svc.SendAll(dest)
				fyne.Do(func() {
					sendAll.Enable()
					if err != nil {
						result.SetText("")
						dialog.ShowError(err, u.win)
						return
					}
					result.SetText("Broadcast: " + txid)
					amount.SetText("")
					to.SetText("")
					u.refresh()
				})
			}()
		}, u.win)
	})

	form := widget.NewForm(
		widget.NewFormItem("To", to),
		widget.NewFormItem("Amount", amount),
		widget.NewFormItem("Unit", unit),
	)
	return container.NewVBox(form, container.NewHBox(send, sendAll), result)
}

func (u *UI) utxosTab() fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, nil, container.NewVScroll(u.utxoBox))
}

func (u *UI) historyTab() fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, nil, container.NewVScroll(u.historyBox))
}

func (u *UI) settingsTab() fyne.CanvasObject {
	lock := widget.NewButtonWithIcon("Lock", theme.LogoutIcon(), func() {
		u.svc.Lock()
		u.showUnlock()
	})
	path := widget.NewLabel(u.svc.WalletPath())
	path.Wrapping = fyne.TextWrapBreak
	return container.NewVBox(
		widget.NewLabelWithStyle("Wallet file", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		path,
		widget.NewSeparator(),
		widget.NewLabel("Default network: testnet"),
		widget.NewLabel("Storage: encrypted local file"),
		lock,
	)
}

func (u *UI) refresh() {
	if u.balanceLabel == nil {
		return
	}
	snap := u.svc.Snapshot()
	u.balanceLabel.SetText("Balance: " + formatSats(snap.BalanceSats))
	u.networkLabel.SetText("Network: " + walletapp.NetworkLabel(snap.Network))
	u.peerLabel.SetText(fmt.Sprintf("Peers: %d", snap.Status.PeerCount))
	u.heightLabel.SetText(fmt.Sprintf("Height: %d", snap.Status.BestPeerHeight))
	addr := snap.ReceiveAddress
	if addr == "" {
		addr = "-"
	}
	u.addressLabel.SetText(addr)
	u.statusLabel.SetText(fmt.Sprintf("Wallet: %s | Fee: %d sat/byte | Store: %s", snap.WalletName, snap.FeePerByte, snap.StorePath))
	u.fillAddresses(snap.Addresses)
	u.fillUTXOs(snap.UTXOs)
	u.fillHistory(snap.History)
}

func (u *UI) addEvent(ev walletapp.Event) {
	if u.eventEntry == nil {
		return
	}
	line := fmt.Sprintf("%s  %s", ev.At.Local().Format("15:04:05"), ev.Message)
	u.events = append([]string{line}, u.events...)
	if len(u.events) > 80 {
		u.events = u.events[:80]
	}
	u.eventEntry.SetText(strings.Join(u.events, "\n"))
}

func (u *UI) fillAddresses(records []store.AddressRecord) {
	if u.addressBox == nil {
		return
	}
	rows := make([]fyne.CanvasObject, 0, len(records))
	for _, rec := range records {
		branch := "receive"
		if rec.Branch == 1 {
			branch = "change"
		}
		label := widget.NewLabel(fmt.Sprintf("%s %d/%d  %s", branch, rec.Branch, rec.Index, rec.Address))
		label.Wrapping = fyne.TextWrapBreak
		rows = append(rows, label, widget.NewSeparator())
	}
	if len(rows) == 0 {
		rows = append(rows, widget.NewLabel("No addresses"))
	}
	u.addressBox.Objects = rows
	u.addressBox.Refresh()
}

func (u *UI) fillUTXOs(records []store.UTXORecord) {
	if u.utxoBox == nil {
		return
	}
	rows := make([]fyne.CanvasObject, 0, len(records))
	for _, rec := range records {
		label := widget.NewLabel(fmt.Sprintf("%s:%d  %s  height %d", short(rec.TxID), rec.Vout, formatSats(rec.Value), rec.Height))
		label.Wrapping = fyne.TextWrapBreak
		rows = append(rows, label, widget.NewSeparator())
	}
	if len(rows) == 0 {
		rows = append(rows, widget.NewLabel("No UTXOs"))
	}
	u.utxoBox.Objects = rows
	u.utxoBox.Refresh()
}

func (u *UI) fillHistory(records []store.TxRecord) {
	if u.historyBox == nil {
		return
	}
	rows := make([]fyne.CanvasObject, 0, len(records))
	for _, rec := range records {
		label := widget.NewLabel(fmt.Sprintf("%s  %s  %s  %s", rec.SeenAt.Local().Format("2006-01-02 15:04"), rec.Direction, formatSats(rec.Amount), short(rec.TxID)))
		label.Wrapping = fyne.TextWrapBreak
		rows = append(rows, label, widget.NewSeparator())
	}
	if len(rows) == 0 {
		rows = append(rows, widget.NewLabel("No history"))
	}
	u.historyBox.Objects = rows
	u.historyBox.Refresh()
}

func networkLabels() []string {
	return []string{"Testnet", "Mainnet", "STN", "Regtest"}
}

func networkFromLabel(label string) string {
	switch label {
	case "Mainnet":
		return walletapp.NetworkMainnet
	case "STN":
		return walletapp.NetworkSTN
	case "Regtest":
		return walletapp.NetworkRegtest
	default:
		return walletapp.NetworkTestnet
	}
}

func parseAmount(input, unit string) (int64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("amount required")
	}
	if unit == "BSV" {
		return parseBSV(input)
	}
	sats, err := strconv.ParseInt(input, 10, 64)
	if err != nil || sats <= 0 {
		return 0, fmt.Errorf("invalid satoshi amount")
	}
	return sats, nil
}

func parseBSV(input string) (int64, error) {
	if strings.HasPrefix(input, "-") {
		return 0, fmt.Errorf("amount must be positive")
	}
	parts := strings.Split(input, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid BSV amount")
	}
	whole := int64(0)
	if parts[0] != "" {
		n, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid BSV amount")
		}
		whole = n
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 8 {
		return 0, fmt.Errorf("BSV amount has more than 8 decimals")
	}
	for len(frac) < 8 {
		frac += "0"
	}
	fraction := int64(0)
	if frac != "" {
		n, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid BSV amount")
		}
		fraction = n
	}
	if whole > math.MaxInt64/100000000 {
		return 0, fmt.Errorf("amount too large")
	}
	sats := whole*100000000 + fraction
	if sats <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	return sats, nil
}

func formatSats(sats int64) string {
	sign := ""
	if sats < 0 {
		sign = "-"
		sats = -sats
	}
	return fmt.Sprintf("%s%d.%08d BSV (%s%d sat)", sign, sats/100000000, sats%100000000, sign, sats)
}

func short(txid string) string {
	if len(txid) <= 16 {
		return txid
	}
	return txid[:8] + "..." + txid[len(txid)-8:]
}
