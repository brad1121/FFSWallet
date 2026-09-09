package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
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
	eventSeen  map[string]struct{}
	eventText  string
	eventSync  bool
	stopScan   *widget.Button
	lastAddrs  []store.AddressRecord
	lastUTXOs  []store.UTXORecord
	lastTxs    []store.TxRecord
	closed     chan struct{}
}

func Run(svc *walletapp.Service) {
	a := fyneapp.NewWithID("com.ffswallet.desktop")
	u := &UI{
		svc:       svc,
		app:       a,
		closed:    make(chan struct{}),
		eventSeen: make(map[string]struct{}),
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

	u.showHome()
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

func (u *UI) showHome() {
	title := widget.NewLabelWithStyle("FFSWallet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	sub := widget.NewLabelWithStyle("Bitcoin SV desktop wallet", fyne.TextAlignCenter, fyne.TextStyle{})
	wallets := u.walletList()

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
		container.NewPadded(container.NewVBox(wallets, widget.NewSeparator(), tabs)),
	))
}

func (u *UI) walletList() fyne.CanvasObject {
	names := u.svc.WalletNames()
	rows := []fyne.CanvasObject{
		widget.NewLabelWithStyle("Wallets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	}
	if len(names) == 0 {
		rows = append(rows, widget.NewLabel("No wallets yet. Create one below."))
		return container.NewVBox(rows...)
	}
	for _, name := range names {
		walletName := name
		rows = append(rows, widget.NewButtonWithIcon(walletName, theme.LoginIcon(), func() {
			if err := u.svc.SelectWallet(walletName); err != nil {
				dialog.ShowError(err, u.win)
				return
			}
			u.showUnlock()
		}))
	}
	return container.NewVBox(rows...)
}

func (u *UI) createWalletTab() fyne.CanvasObject {
	name := widget.NewEntry()
	name.SetPlaceHolder("Wallet name")
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
				mnemonic, err := u.svc.CreateWallet(name.Text, pass.Text, networkFromLabel(network.Selected))
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
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Network", network),
		widget.NewFormItem("Passphrase", pass),
		widget.NewFormItem("Confirm", confirm),
	)
	return container.NewVBox(form, create, result)
}

func (u *UI) seedWalletTab() fyne.CanvasObject {
	name := widget.NewEntry()
	name.SetPlaceHolder("Wallet name")
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
				err := u.svc.CreateWalletFromSeed(name.Text, mnemonic.Text, pass.Text, networkName)
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
		widget.NewFormItem("Name", name),
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
	startHeight := widget.NewEntry()
	startHeight.SetPlaceHolder("Start block height (optional)")
	if height := walletapp.DefaultRescanStartHeight(network); height > 0 {
		startHeight.SetText(strconv.FormatInt(int64(height), 10))
	}
	bindDefaultRescanHeight(startHash, startHeight, network)
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
		height, err := parseRescanHeight(startHeight.Text)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		hash := startHash.Text
		rescan.Disable()
		skip.Disable()
		status.SetText("Rescanning wallet. Keep app open.")
		go func() {
			var msg string
			var err error
			msg, err = u.svc.RescanFromBlockHashAtHeight(hash, height)
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
		widget.NewForm(widget.NewFormItem("Start hash", startHash), widget.NewFormItem("Start height", startHeight)),
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
	name := u.svc.WalletName()
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
	pass.OnSubmitted = func(string) {
		unlock.OnTapped()
	}
	u.win.SetContent(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("FFSWallet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Unlock wallet", fyne.TextAlignCenter, fyne.TextStyle{}),
		widget.NewLabel("Wallet: "+name),
		pass,
		widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), func() { u.showHome() }),
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
	u.eventEntry.Wrapping = fyne.TextWrapBreak
	u.eventEntry.SetPlaceHolder("Wallet and peer events appear here. Select text to copy.")
	u.eventEntry.OnChanged = func(text string) {
		if u.eventSync || text == u.eventText {
			return
		}
		u.setEventText(u.eventText)
	}
	u.setEventText(strings.Join(u.events, "\n"))

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
	trafficToggle := widget.NewCheck("P2P traffic", nil)
	trafficToggle.SetChecked(u.svc.P2PTrafficEnabled())
	trafficToggle.OnChanged = func(enabled bool) {
		u.svc.SetP2PTrafficEnabled(enabled)
	}
	eventsHeader := container.NewBorder(
		nil,
		nil,
		widget.NewLabelWithStyle("Events", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		trafficToggle,
	)
	return container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("Current receive address", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			u.addressLabel,
			container.NewHBox(newAddr, copyAddr),
			widget.NewSeparator(),
			eventsHeader,
		),
		nil,
		nil,
		nil,
		container.NewVScroll(u.eventEntry),
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
	help := widget.NewLabel("Double-click a UTXO row for details and transaction link.")
	help.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewVBox(help, widget.NewSeparator()), nil, nil, nil, container.NewVScroll(u.utxoBox))
}

func (u *UI) historyTab() fyne.CanvasObject {
	help := widget.NewLabel("Double-click a history row for transaction details and transaction link.")
	help.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewVBox(help, widget.NewSeparator()), nil, nil, nil, container.NewVScroll(u.historyBox))
}

func (u *UI) settingsTab() fyne.CanvasObject {
	lock := widget.NewButtonWithIcon("Lock and switch wallet", theme.LogoutIcon(), func() {
		u.svc.Lock()
		u.showHome()
	})
	name := widget.NewLabel(u.svc.WalletName())
	path := widget.NewLabel(u.svc.WalletPath())
	path.Wrapping = fyne.TextWrapBreak
	snap := u.svc.Snapshot()
	feeEntry := widget.NewEntry()
	feeEntry.SetPlaceHolder("sat/byte")
	feeEntry.SetText(strconv.FormatInt(snap.FeePerByte, 10))
	feeStatus := widget.NewLabel("")
	feeStatus.Wrapping = fyne.TextWrapWord
	saveFee := widget.NewButtonWithIcon("Save fee", theme.DocumentSaveIcon(), nil)
	saveFee.OnTapped = func() {
		fee, err := strconv.ParseInt(strings.TrimSpace(feeEntry.Text), 10, 64)
		if err != nil || fee <= 0 {
			dialog.ShowError(fmt.Errorf("fee must be a positive integer in sat/byte"), u.win)
			return
		}
		saveFee.Disable()
		feeStatus.SetText("Saving fee rate...")
		go func() {
			err := u.svc.SetFeePerByte(fee)
			fyne.Do(func() {
				saveFee.Enable()
				if err != nil {
					feeStatus.SetText("")
					dialog.ShowError(err, u.win)
					return
				}
				feeStatus.SetText(fmt.Sprintf("Fee rate saved: %d sat/byte", fee))
				u.refresh()
			})
		}()
	}
	feeEntry.OnSubmitted = func(string) {
		saveFee.OnTapped()
	}
	peerAddr := widget.NewEntry()
	peerAddr.SetPlaceHolder("host:port")
	peerStatus := widget.NewLabel("")
	peerStatus.Wrapping = fyne.TextWrapWord
	addPeer := widget.NewButtonWithIcon("Add peer", theme.ContentAddIcon(), nil)
	addPeer.OnTapped = func() {
		addr := strings.TrimSpace(peerAddr.Text)
		if addr == "" {
			dialog.ShowError(fmt.Errorf("peer address required"), u.win)
			return
		}
		addPeer.Disable()
		peerStatus.SetText("Dialing peer...")
		go func() {
			err := u.svc.AddPeer(addr)
			fyne.Do(func() {
				addPeer.Enable()
				if err != nil {
					peerStatus.SetText("")
					dialog.ShowError(err, u.win)
					return
				}
				peerStatus.SetText("Peer dial started: " + addr)
				peerAddr.SetText("")
			})
		}()
	}
	peerAddr.OnSubmitted = func(string) {
		addPeer.OnTapped()
	}
	rescanHash := widget.NewEntry()
	rescanHash.SetPlaceHolder("64-character start block hash")
	if defaultHash := walletapp.DefaultRescanStartHash(snap.Network); defaultHash != "" {
		rescanHash.SetText(defaultHash)
	}
	rescanHeight := widget.NewEntry()
	rescanHeight.SetPlaceHolder("Start block height (optional)")
	if height := walletapp.DefaultRescanStartHeight(snap.Network); height > 0 {
		rescanHeight.SetText(strconv.FormatInt(int64(height), 10))
	}
	bindDefaultRescanHeight(rescanHash, rescanHeight, snap.Network)
	rescanHelp := widget.NewLabel(walletapp.DefaultRescanStartLabel(snap.Network))
	rescanHelp.Wrapping = fyne.TextWrapWord
	rescanRebuild := widget.NewCheck("Rebuild UTXOs first", nil)
	rescanRebuild.SetChecked(true)
	rescanRebuildHelp := widget.NewLabel("Recommended for stale balances, missing-input transactions, or local unconfirmed change that peers never accepted.")
	rescanRebuildHelp.Wrapping = fyne.TextWrapWord
	rescanStatus := widget.NewLabel("")
	rescanStatus.Wrapping = fyne.TextWrapWord
	var startRescan *widget.Button
	startRescan = widget.NewButtonWithIcon("Start rescan", theme.ViewRefreshIcon(), nil)
	startRescan.OnTapped = func() {
		hash := strings.TrimSpace(rescanHash.Text)
		if hash == "" {
			dialog.ShowError(fmt.Errorf("start block hash required"), u.win)
			return
		}
		height, err := parseRescanHeight(rescanHeight.Text)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		run := func() {
			startRescan.Disable()
			rescanStatus.SetText("Rescan running. Keep wallet open.")
			go func() {
				var msg string
				var err error
				if rescanRebuild.Checked {
					msg, err = u.svc.RebuildFromBlockHashAtHeight(hash, height)
				} else {
					msg, err = u.svc.RescanFromBlockHashAtHeight(hash, height)
				}
				fyne.Do(func() {
					startRescan.Enable()
					if err != nil {
						rescanStatus.SetText("")
						dialog.ShowError(err, u.win)
						return
					}
					rescanStatus.SetText(msg)
					u.refresh()
				})
			}()
		}
		if !rescanRebuild.Checked {
			run()
			return
		}
		dialog.ShowConfirm("Confirm rebuild rescan", "This clears local UTXOs before scanning blocks, then rebuilds wallet balance from confirmed chain data. Continue?", func(ok bool) {
			if ok {
				run()
			}
		}, u.win)
	}
	rescanHash.OnSubmitted = func(string) {
		startRescan.OnTapped()
	}
	// A scan runs until it reaches the tip, which can be hours. Stopping keeps
	// every block replayed so far; only progress since the last checkpoint is
	// lost, and the next catch-up resumes from there.
	stopScan := widget.NewButtonWithIcon("Stop scan", theme.CancelIcon(), func() {
		if err := u.svc.StopScan(); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		rescanStatus.SetText("Stopping scan...")
	})
	stopScan.Importance = widget.DangerImportance
	if !u.svc.ScanRunning() {
		stopScan.Disable()
	}
	u.stopScan = stopScan
	rebroadcastStatus := widget.NewLabel("")
	rebroadcastStatus.Wrapping = fyne.TextWrapWord
	var rebroadcast *widget.Button
	rebroadcast = widget.NewButtonWithIcon("Rebroadcast pending", theme.ViewRefreshIcon(), func() {
		rebroadcast.Disable()
		rebroadcastStatus.SetText("Rebroadcasting pending transactions...")
		go func() {
			count, err := u.svc.RebroadcastPending()
			fyne.Do(func() {
				rebroadcast.Enable()
				if err != nil {
					rebroadcastStatus.SetText("")
					dialog.ShowError(err, u.win)
					return
				}
				if count == 0 {
					rebroadcastStatus.SetText("No stored pending transactions to rebroadcast.")
					return
				}
				rebroadcastStatus.SetText(fmt.Sprintf("Rebroadcast %d pending transaction(s).", count))
			})
		}()
	})
	return container.NewVBox(
		widget.NewLabelWithStyle("Wallet name", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		name,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Wallet file", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		path,
		widget.NewSeparator(),
		widget.NewLabel("Default network: testnet"),
		widget.NewLabel("Storage: encrypted local file"),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Transaction fee", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Manual fee rate in satoshis per byte."),
		feeEntry,
		saveFee,
		feeStatus,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Manual peer dial", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Add a peer directly using host:port."),
		peerAddr,
		addPeer,
		peerStatus,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Wallet rescan", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		rescanHelp,
		rescanHash,
		rescanHeight,
		rescanRebuild,
		rescanRebuildHelp,
		container.NewHBox(startRescan, stopScan),
		rescanStatus,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Pending transactions", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Push stored broadcast transactions to connected peers again."),
		rebroadcast,
		rebroadcastStatus,
		widget.NewSeparator(),
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
	u.heightLabel.SetText(formatHeight(snap.Status.ChainHeight, snap.Status.BestPeerHeight, snap.SyncedHeight, snap.ScanRate))
	if u.stopScan != nil {
		if snap.Scanning {
			u.stopScan.Enable()
		} else {
			u.stopScan.Disable()
		}
	}
	addr := snap.ReceiveAddress
	if addr == "" {
		addr = "-"
	}
	u.addressLabel.SetText(addr)
	u.statusLabel.SetText(fmt.Sprintf("Wallet: %s | Fee: %d sat/byte | Store: %s", snap.WalletName, snap.FeePerByte, snap.StorePath))
	if !reflect.DeepEqual(u.lastAddrs, snap.Addresses) {
		u.fillAddresses(snap.Addresses)
		u.lastAddrs = append([]store.AddressRecord(nil), snap.Addresses...)
	}
	if !reflect.DeepEqual(u.lastUTXOs, snap.UTXOs) {
		u.fillUTXOs(snap.UTXOs)
		u.lastUTXOs = append([]store.UTXORecord(nil), snap.UTXOs...)
	}
	if !reflect.DeepEqual(u.lastTxs, snap.History) {
		u.fillHistory(snap.History)
		u.lastTxs = append([]store.TxRecord(nil), snap.History...)
	}
}

func (u *UI) addEvent(ev walletapp.Event) {
	if u.eventEntry == nil {
		return
	}
	if ev.Type == walletapp.EventPayment {
		key := string(ev.Type) + ":" + ev.Message
		if _, exists := u.eventSeen[key]; exists {
			return
		}
		u.eventSeen[key] = struct{}{}
	}
	line := fmt.Sprintf("%s  %s", ev.At.Local().Format("15:04:05"), ev.Message)
	u.events = append([]string{line}, u.events...)
	if len(u.events) > 1000 {
		u.events = u.events[:1000]
	}
	u.setEventText(strings.Join(u.events, "\n"))
}

func (u *UI) setEventText(text string) {
	u.eventText = text
	if u.eventEntry == nil {
		return
	}
	u.eventSync = true
	u.eventEntry.SetText(text)
	u.eventSync = false
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
		utxo := rec
		button := newDoubleTapButton(fmt.Sprintf("%s:%d  %s  height %d", short(rec.TxID), rec.Vout, formatSats(rec.Value), rec.Height), func() {
			u.showUTXODetail(utxo)
		})
		button.Alignment = widget.ButtonAlignLeading
		button.Importance = widget.LowImportance
		rows = append(rows, button, widget.NewSeparator())
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
		tx := rec
		button := newDoubleTapButton(fmt.Sprintf("%s  %s  %s  %s  %s", rec.SeenAt.Local().Format("2006-01-02 15:04"), rec.Direction, formatSats(rec.Amount), rec.Status, short(rec.TxID)), func() {
			u.showHistoryDetail(tx)
		})
		button.Alignment = widget.ButtonAlignLeading
		button.Importance = widget.LowImportance
		rows = append(rows, button, widget.NewSeparator())
	}
	if len(rows) == 0 {
		rows = append(rows, widget.NewLabel("No history"))
	}
	u.historyBox.Objects = rows
	u.historyBox.Refresh()
}

func (u *UI) showUTXODetail(rec store.UTXORecord) {
	snap := u.svc.Snapshot()
	content := []fyne.CanvasObject{
		widget.NewLabelWithStyle("UTXO details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("TxID", selectableText(rec.TxID)),
			widget.NewFormItem("Vout", selectableText(strconv.FormatUint(uint64(rec.Vout), 10))),
			widget.NewFormItem("Value", selectableText(formatSats(rec.Value))),
			widget.NewFormItem("Height", selectableText(strconv.FormatInt(int64(rec.Height), 10))),
			widget.NewFormItem("Seen", selectableText(rec.SeenAt.Local().Format("2006-01-02 15:04:05"))),
		),
	}
	if rec.ScriptHex != "" {
		script := codeBlock(rec.ScriptHex)
		detailJSON := codeBlock(utxoDetailJSON(rec, snap.History))

		accordion := widget.NewAccordion(
			widget.NewAccordionItem("Decoded transaction details", detailJSON),
		)
		content = append(content,
			widget.NewLabelWithStyle("Script", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			script,
			accordion,
		)
	}
	if link := whatsOnChainTxURL(snap.Network, rec.TxID); link != nil {
		content = append(content, widget.NewHyperlink("Open transaction in WhatsOnChain", link))
	}
	dlg := dialog.NewCustom("UTXO details", "Close", container.NewVScroll(container.NewVBox(content...)), u.win)
	size := u.win.Canvas().Size()
	dlg.Resize(fyne.NewSize(size.Width*0.8, size.Height*0.8))
	dlg.Show()
}

func (u *UI) showHistoryDetail(rec store.TxRecord) {
	snap := u.svc.Snapshot()
	items := []*widget.FormItem{
		widget.NewFormItem("TxID", selectableText(rec.TxID)),
		widget.NewFormItem("Direction", selectableText(rec.Direction)),
		widget.NewFormItem("Address", selectableText(rec.Address)),
		widget.NewFormItem("Amount", selectableText(formatSats(rec.Amount))),
		widget.NewFormItem("Height", selectableText(strconv.FormatInt(int64(rec.Height), 10))),
		widget.NewFormItem("Status", selectableText(rec.Status)),
		widget.NewFormItem("Seen", selectableText(rec.SeenAt.Local().Format("2006-01-02 15:04:05"))),
	}
	if rec.Direction == "in" || rec.Vout != 0 {
		items = append(items, widget.NewFormItem("Vout", selectableText(strconv.FormatUint(uint64(rec.Vout), 10))))
	}
	if rec.Note != "" {
		items = append(items, widget.NewFormItem("Note", selectableText(rec.Note)))
	}

	content := []fyne.CanvasObject{
		widget.NewLabelWithStyle("Transaction details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(items...),
	}

	accordion := widget.NewAccordion(
		widget.NewAccordionItem("Decoded wallet details", codeBlock(historyDetailJSON(rec, snap.UTXOs))),
	)
	accordion.Open(0)
	content = append(content, accordion)
	if rec.RawHex != "" {
		content = append(content,
			widget.NewLabelWithStyle("Raw transaction hex", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			codeBlock(rec.RawHex),
		)
	}
	if link := whatsOnChainTxURL(snap.Network, rec.TxID); link != nil {
		content = append(content, widget.NewHyperlink("Open transaction in WhatsOnChain", link))
	}
	dlg := dialog.NewCustom("Transaction details", "Close", container.NewVScroll(container.NewVBox(content...)), u.win)
	size := u.win.Canvas().Size()
	dlg.Resize(fyne.NewSize(size.Width*0.8, size.Height*0.8))
	dlg.Show()
}

func utxoDetailJSON(rec store.UTXORecord, history []store.TxRecord) string {
	matched := make([]store.TxRecord, 0, 2)
	for _, tx := range history {
		if tx.TxID == rec.TxID {
			matched = append(matched, tx)
		}
	}
	payload := struct {
		TxID       string           `json:"txid"`
		Vout       uint32           `json:"vout"`
		ValueSats  int64            `json:"value_sats"`
		ValueBSV   string           `json:"value_bsv"`
		Height     int32            `json:"height"`
		SeenAt     time.Time        `json:"seen_at"`
		ScriptHex  string           `json:"script_hex"`
		WalletHits []store.TxRecord `json:"wallet_history_matches,omitempty"`
	}{
		TxID:       rec.TxID,
		Vout:       rec.Vout,
		ValueSats:  rec.Value,
		ValueBSV:   fmt.Sprintf("%.8f", float64(rec.Value)/100000000),
		Height:     rec.Height,
		SeenAt:     rec.SeenAt,
		ScriptHex:  rec.ScriptHex,
		WalletHits: matched,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\n  \"error\": %q\n}", err.Error())
	}
	return string(raw)
}

func historyDetailJSON(rec store.TxRecord, utxos []store.UTXORecord) string {
	matched := make([]store.UTXORecord, 0, 2)
	for _, utxo := range utxos {
		if utxo.TxID == rec.TxID {
			matched = append(matched, utxo)
		}
	}
	payload := struct {
		History     store.TxRecord     `json:"history"`
		AmountBSV   string             `json:"amount_bsv"`
		WalletUTXOs []store.UTXORecord `json:"wallet_utxos,omitempty"`
	}{
		History:     rec,
		AmountBSV:   fmt.Sprintf("%.8f", float64(rec.Amount)/100000000),
		WalletUTXOs: matched,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\n  \"error\": %q\n}", err.Error())
	}
	return string(raw)
}

func codeBlock(text string) fyne.CanvasObject {
	return widget.NewCard("", "", selectableMultilineText(text))
}

func selectableText(text string) *widget.Entry {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapBreak
	entry.SetMinRowsVisible(1)
	entry.SetText(text)
	makeReadOnlyEntry(entry, text)
	return entry
}

func selectableMultilineText(text string) *widget.Entry {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapBreak
	entry.SetText(text)
	makeReadOnlyEntry(entry, text)
	return entry
}

func makeReadOnlyEntry(entry *widget.Entry, text string) {
	var syncing bool
	entry.OnChanged = func(value string) {
		if syncing || value == text {
			return
		}
		syncing = true
		entry.SetText(text)
		syncing = false
	}
}

func whatsOnChainTxURL(network, txid string) *url.URL {
	var raw string
	switch walletapp.NormalizeNetwork(network) {
	case walletapp.NetworkMainnet:
		raw = "https://whatsonchain.com/tx/" + txid
	case walletapp.NetworkTestnet:
		raw = "https://test.whatsonchain.com/tx/" + txid
	case walletapp.NetworkSTN:
		raw = "https://stn.whatsonchain.com/tx/" + txid
	default:
		return nil
	}
	link, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return link
}

type doubleTapButton struct {
	widget.Button
	onDoubleTap func()
}

func newDoubleTapButton(label string, onDoubleTap func()) *doubleTapButton {
	b := &doubleTapButton{onDoubleTap: onDoubleTap}
	b.Text = label
	b.OnTapped = func() {}
	b.ExtendBaseWidget(b)
	return b
}

func (b *doubleTapButton) DoubleTapped(_ *fyne.PointEvent) {
	if b.onDoubleTap != nil {
		b.onDoubleTap()
	}
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

func parseRescanHeight(input string) (int32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, nil
	}
	height, err := strconv.ParseInt(input, 10, 32)
	if err != nil || height < 0 {
		return 0, fmt.Errorf("invalid start block height")
	}
	return int32(height), nil
}

func bindDefaultRescanHeight(hashEntry, heightEntry *widget.Entry, network string) {
	defaultHash := walletapp.DefaultRescanStartHash(network)
	defaultHeight := walletapp.DefaultRescanStartHeight(network)
	if defaultHash == "" || defaultHeight == 0 {
		return
	}
	defaultHeightText := strconv.FormatInt(int64(defaultHeight), 10)
	hashEntry.OnChanged = func(value string) {
		hash := strings.ToLower(strings.TrimSpace(value))
		height := strings.TrimSpace(heightEntry.Text)
		if hash == defaultHash {
			if height == "" {
				heightEntry.SetText(defaultHeightText)
			}
			return
		}
		if height == defaultHeightText {
			heightEntry.SetText("")
		}
	}
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

// formatHeight renders the wallet's followed header tip alongside the best
// height advertised by peers. When peers are ahead the wallet is still
// catching up, so show both as "chain / peer (syncing)"; otherwise show the
// single synced height.
func formatHeight(chain, peer, synced int32, scanRate float64) string {
	base := "Height: —"
	switch {
	case chain < 0 && peer < 0:
	case peer > chain:
		base = fmt.Sprintf("Height: %d / %d (syncing)", chain, peer)
	default:
		base = fmt.Sprintf("Height: %d", chain)
	}
	if synced <= 0 {
		// No cursor yet: the wallet has never completed a scan, so it only
		// knows about payments relayed while it was open.
		return base + " · not scanned"
	}
	if peer > synced {
		behind := peer - synced
		return base + fmt.Sprintf(" · scanned to %d (%d behind%s)", synced, behind, formatScanRate(scanRate, behind))
	}
	return base + fmt.Sprintf(" · scanned to %d%s", synced, formatScanRate(scanRate, 0))
}

// formatScanRate renders the live scan speed, and how long the remaining
// blocks will take at that speed — the number that answers whether a scan is
// worth waiting for. Empty until a rate has actually been measured.
func formatScanRate(rate float64, behind int32) string {
	if rate <= 0 {
		return ""
	}
	out := fmt.Sprintf(", %s blk/s", trimRate(rate))
	if behind > 0 {
		out += ", " + formatETA(time.Duration(float64(behind)/rate)*time.Second)
	}
	return out
}

func trimRate(rate float64) string {
	if rate >= 10 {
		return strconv.FormatFloat(rate, 'f', 0, 64)
	}
	return strconv.FormatFloat(rate, 'f', 1, 64)
}

func formatETA(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("~%dd left", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("~%dh%02dm left", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("~%dm left", int(d.Minutes()))
	default:
		return "<1m left"
	}
}

func short(txid string) string {
	if len(txid) <= 16 {
		return txid
	}
	return txid[:8] + "..." + txid[len(txid)-8:]
}
