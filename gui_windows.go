package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type actionModel struct {
	walk.TableModelBase
	items []Act
}

func (m *actionModel) RowCount() int { return len(m.items) }

func (m *actionModel) Value(row, col int) interface{} {
	a := m.items[row]
	switch col {
	case 0:
		return a.ID
	case 1:
		return a.Name
	case 2:
		return typeLabels[a.Type]
	case 3:
		return a.Summary()
	}
	return ""
}

type gui struct {
	b       *Bridge
	trigger *TriggerServer
	model   *actionModel

	mw          *walk.MainWindow
	ipEdit      *walk.LineEdit
	stationEdit *walk.LineEdit
	connectBtn  *walk.PushButton
	statusLbl   *walk.Label
	tv          *walk.TableView
	lastEdit    *walk.LineEdit

	escAction *walk.Action

	logWin  *walk.MainWindow
	logEdit *walk.TextEdit

	// Log de-duplication; only touched on the GUI thread.
	lastMsg string
	repeats int

	wantConn bool
}

func runApp() {
	path := configPath()
	cfg, cfgErr := LoadConfig(path)
	g := &gui{b: NewBridge(cfg, path), model: &actionModel{items: cfg.Actions}}
	g.b.Logf = g.logf
	g.b.Flex.Logf = g.logf
	g.b.Flex.OnState = func(connected bool, text string) {
		g.mw.Synchronize(func() { g.statusLbl.SetText(text) })
	}

	actionItems := func() []MenuItem {
		return []MenuItem{
			Action{Text: "&Add...", OnTriggered: g.onAdd},
			Action{Text: "&Edit...", OnTriggered: g.onEdit},
			Action{Text: "D&uplicate", OnTriggered: g.onDuplicate},
			Action{Text: "&Delete", OnTriggered: g.onDelete},
			Separator{},
			Action{Text: "&Test", OnTriggered: g.onTest},
			Action{Text: "&Copy N1MM macro", OnTriggered: g.onCopyMacro},
		}
	}

	// The main window is kept small: it stays on screen while operating.
	err := MainWindow{
		AssignTo: &g.mw,
		Title:    "SliceTrigger",
		MinSize:  Size{Width: 360, Height: 140},
		Size:     Size{Width: 520, Height: 210},
		MenuItems: []MenuItem{
			Menu{Text: "&Action", Items: actionItems()},
			Menu{Text: "&Settings", Items: []MenuItem{
				Action{Text: "Trigger &port...", OnTriggered: g.onTriggerPort},
				Action{AssignTo: &g.escAction, Text: "&ESC stops voice keyer playback",
					Checkable: true, Checked: cfg.EscStopsDVK, OnTriggered: g.onEscToggle},
			}},
			Action{Text: "&Log", OnTriggered: g.showLog},
		},
		Layout: VBox{Margins: Margins{Left: 4, Top: 4, Right: 4, Bottom: 4}, Spacing: 4},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 4},
				Children: []Widget{
					LineEdit{AssignTo: &g.ipEdit, Text: cfg.RadioIP, MinSize: Size{Width: 95}, MaxSize: Size{Width: 110},
						ToolTipText: "Radio IP address"},
					PushButton{Text: "Discover", OnClicked: g.onDiscover},
					LineEdit{AssignTo: &g.stationEdit, Text: cfg.Station, MinSize: Size{Width: 70}, MaxSize: Size{Width: 110},
						ToolTipText: "Optional SmartSDR station name (multiFLEX). Empty = any client."},
					PushButton{AssignTo: &g.connectBtn, Text: "Connect", OnClicked: g.onConnect},
					Label{AssignTo: &g.statusLbl, Text: "Not connected"},
					HSpacer{},
				},
			},
			TableView{
				AssignTo:         &g.tv,
				AlternatingRowBG: true,
				Columns: []TableViewColumn{
					{Title: "ID", Width: 32},
					{Title: "Name", Width: 130},
					{Title: "Type", Width: 105},
					{Title: "Summary", Width: 220},
				},
				Model:            g.model,
				OnItemActivated:  g.onEdit,
				ContextMenuItems: actionItems(),
			},
			LineEdit{AssignTo: &g.lastEdit, ReadOnly: true, ToolTipText: "Last message. Open Log for the history."},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, "SliceTrigger", "Cannot create window: "+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	// The log lives in its own window so it can sit on another screen.
	err = MainWindow{
		AssignTo: &g.logWin,
		Title:    "SliceTrigger Log",
		Visible:  false,
		MinSize:  Size{Width: 300, Height: 150},
		Size:     Size{Width: 720, Height: 320},
		Layout:   VBox{MarginsZero: true},
		Children: []Widget{
			TextEdit{AssignTo: &g.logEdit, ReadOnly: true, VScroll: true},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(g.mw, "SliceTrigger", "Cannot create log window: "+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}
	// Closing the log window only hides it.
	g.logWin.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		g.logWin.Hide()
	})

	g.logf("Config: %s", path)
	if cfgErr != nil {
		g.logf("Config could not be read, using defaults: %v", cfgErr)
	}
	g.startTrigger(cfg.Port)
	g.applyEscHook(cfg.EscStopsDVK)
	if cfg.AutoConnect && cfg.RadioIP != "" {
		g.connect()
	}
	g.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		removeEscHook()
		g.b.Flex.OnState = nil
		g.b.Flex.Disconnect()
		if g.trigger != nil {
			g.trigger.Close()
		}
	})
	g.mw.Run()
}

func (g *gui) applyEscHook(on bool) {
	if !on {
		removeEscHook()
		return
	}
	err := installEscHook(func() {
		if msg, err := g.b.StopDVK(true); err != nil {
			g.logf("ESC: %s %v", msg, err)
		} else if msg != "" {
			g.logf("ESC: %s", msg)
		}
	})
	if err != nil {
		g.logf("ESC key hook could not be installed: %v", err)
	}
}

func (g *gui) onEscToggle() {
	on := g.escAction.Checked()
	g.save(func(c *Config) { c.EscStopsDVK = on })
	g.applyEscHook(on)
}

func (g *gui) showLog() {
	g.logWin.Show()
	g.logWin.BringToTop()
}

// logf writes to the log window and the one-line display in the main
// window. A message identical to the previous one is not written again; the
// number of repeats is noted when the next different message arrives.
func (g *gui) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	now := time.Now().Format("15:04:05 ")
	if g.mw == nil {
		return
	}
	g.mw.Synchronize(func() {
		if g.logEdit == nil {
			return
		}
		if msg == g.lastMsg {
			g.repeats++
			g.lastEdit.SetText(fmt.Sprintf("%s%s  (x%d)", now, msg, g.repeats+1))
			return
		}
		if g.logEdit.TextLength() > 200000 {
			g.logEdit.SetText("")
		}
		if g.repeats > 0 {
			g.logEdit.AppendText(fmt.Sprintf("         (repeated %d more times)\r\n", g.repeats))
		}
		g.lastMsg, g.repeats = msg, 0
		g.logEdit.AppendText(now + msg + "\r\n")
		g.lastEdit.SetText(now + msg)
	})
}

func (g *gui) save(fn func(c *Config)) {
	if err := g.b.Update(fn); err != nil {
		g.logf("Saving config failed: %v", err)
	}
}

func (g *gui) startTrigger(port int) {
	if g.trigger != nil {
		g.trigger.Close()
		g.trigger = nil
	}
	t, err := StartTrigger(port, g.b)
	if err != nil {
		g.logf("Trigger listener failed: %v", err)
		walk.MsgBox(g.mw, "SliceTrigger",
			fmt.Sprintf("Cannot listen on 127.0.0.1:%d.\n\n%v\n\nIs SliceTrigger already running?\n\n"+
				"This copy was started with arguments: %q\n(a function key should start it with \"fire <id>\")",
				port, err, os.Args[1:]),
			walk.MsgBoxIconWarning)
		return
	}
	g.trigger = t
	g.logf("Listening on http://127.0.0.1:%d/fire/<id>", port)
}

func (g *gui) onTriggerPort() {
	var dlg *walk.Dialog
	var portEdit *walk.NumberEdit
	var okPB, cancelPB *walk.PushButton
	cur := g.b.Config().Port
	err := Dialog{
		AssignTo:      &dlg,
		Title:         "Trigger port",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 300, Height: 120},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: "Local port the fire command connects to (default 8765):"},
			NumberEdit{AssignTo: &portEdit, Value: float64(cur), Decimals: 0, MinValue: 1, MaxValue: 65535},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okPB, Text: "OK", OnClicked: func() { dlg.Accept() }},
				PushButton{AssignTo: &cancelPB, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(g.mw)
	if err != nil || dlg.Run() != walk.DlgCmdOK {
		return
	}
	port := int(portEdit.Value())
	if port == cur && g.trigger != nil {
		return
	}
	g.save(func(c *Config) { c.Port = port })
	g.startTrigger(port)
}

func (g *gui) connect() {
	ip := strings.TrimSpace(g.ipEdit.Text())
	station := strings.TrimSpace(g.stationEdit.Text())
	if ip == "" {
		walk.MsgBox(g.mw, "SliceTrigger", "Enter the radio IP address or use Discover.", walk.MsgBoxIconInformation)
		return
	}
	g.wantConn = true
	g.connectBtn.SetText("Disconnect")
	g.save(func(c *Config) { c.RadioIP, c.Station, c.AutoConnect = ip, station, true })
	g.b.Flex.Connect(ip)
}

func (g *gui) onConnect() {
	if g.wantConn {
		g.wantConn = false
		g.connectBtn.SetText("Connect")
		g.save(func(c *Config) { c.AutoConnect = false })
		g.b.Flex.Disconnect()
		return
	}
	g.connect()
}

func (g *gui) onDiscover() {
	g.logf("Listening for radio discovery broadcasts (3 s)...")
	go func() {
		radios, err := Discover(3 * time.Second)
		g.mw.Synchronize(func() {
			switch {
			case err != nil:
				g.logf("Discovery failed: %v", err)
			case len(radios) == 0:
				g.logf("No radios found")
			case len(radios) == 1:
				g.logf("Found %s", radios[0])
				g.ipEdit.SetText(radios[0].IP)
			default:
				if r, ok := chooseRadio(g.mw, radios); ok {
					g.ipEdit.SetText(r.IP)
				}
			}
		})
	}()
}

func chooseRadio(owner walk.Form, radios []Radio) (Radio, bool) {
	var dlg *walk.Dialog
	var lb *walk.ListBox
	var okPB, cancelPB *walk.PushButton
	names := make([]string, len(radios))
	for i, r := range radios {
		names[i] = r.String()
	}
	err := Dialog{
		AssignTo:      &dlg,
		Title:         "Select radio",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 360, Height: 220},
		Layout:        VBox{},
		Children: []Widget{
			ListBox{AssignTo: &lb, Model: names, OnItemActivated: func() { dlg.Accept() }},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okPB, Text: "OK", OnClicked: func() { dlg.Accept() }},
				PushButton{AssignTo: &cancelPB, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(owner)
	if err != nil {
		return Radio{}, false
	}
	lb.SetCurrentIndex(0)
	if dlg.Run() != walk.DlgCmdOK || lb.CurrentIndex() < 0 {
		return Radio{}, false
	}
	return radios[lb.CurrentIndex()], true
}

// setActions replaces the action list, saves it and reselects id.
func (g *gui) setActions(items []Act, selectID int) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	g.save(func(c *Config) { c.Actions = append([]Act(nil), items...) })
	g.model.items = items
	g.model.PublishRowsReset()
	for i, a := range items {
		if a.ID == selectID {
			g.tv.SetCurrentIndex(i)
		}
	}
}

func (g *gui) selected() (int, bool) {
	i := g.tv.CurrentIndex()
	if i < 0 || i >= len(g.model.items) {
		return 0, false
	}
	return i, true
}

func (g *gui) nextID() int {
	id := 1
	for _, a := range g.model.items {
		if a.ID >= id {
			id = a.ID + 1
		}
	}
	return id
}

func (g *gui) usedIDs(except int) map[int]bool {
	used := map[int]bool{}
	for _, a := range g.model.items {
		if a.ID != except {
			used[a.ID] = true
		}
	}
	return used
}

func (g *gui) onAdd() {
	a := Act{
		ID: g.nextID(), Type: TypeFlexRxAnt, Slice: "Active", Antenna: "ANT1", Cycle: "RX_A,RX_B",
		Host: "192.168.1.199", Mode: KMOn, Relay: 1, User: "admin", Pass: "admin",
		TimeoutMs: 2000,
	}
	if a, ok := editAction(g.mw, a, g.usedIDs(-1), "Add action"); ok {
		g.setActions(append(append([]Act(nil), g.model.items...), a), a.ID)
	}
}

func (g *gui) onEdit() {
	i, ok := g.selected()
	if !ok {
		return
	}
	old := g.model.items[i]
	if a, ok := editAction(g.mw, old, g.usedIDs(old.ID), "Edit action"); ok {
		items := append([]Act(nil), g.model.items...)
		items[i] = a
		g.setActions(items, a.ID)
	}
}

func (g *gui) onDuplicate() {
	i, ok := g.selected()
	if !ok {
		return
	}
	a := g.model.items[i]
	a.ID = g.nextID()
	a.Name += " copy"
	g.setActions(append(append([]Act(nil), g.model.items...), a), a.ID)
}

func (g *gui) onDelete() {
	i, ok := g.selected()
	if !ok {
		return
	}
	a := g.model.items[i]
	if walk.MsgBox(g.mw, "Delete action", fmt.Sprintf("Delete action %d \"%s\"?", a.ID, a.Name),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	items := append([]Act(nil), g.model.items[:i]...)
	g.setActions(append(items, g.model.items[i+1:]...), -1)
}

func (g *gui) onTest() {
	i, ok := g.selected()
	if !ok {
		return
	}
	a := g.model.items[i]
	if a.Type == TypeFlexDVK && a.DVKOp != DVKStop {
		if walk.MsgBox(g.mw, "Test voice keyer",
			fmt.Sprintf("Playing memory %d keys the transmitter.\n\nTransmit now?", a.Memory),
			walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2) != walk.DlgCmdYes {
			return
		}
	}
	go g.b.Fire(strconv.Itoa(a.ID))
}

func (g *gui) onCopyMacro() {
	i, ok := g.selected()
	if !ok {
		return
	}
	a := g.model.items[i]
	exe, err := os.Executable()
	if err != nil {
		g.logf("Cannot determine exe path: %v", err)
		return
	}
	label := a.Name
	if label == "" {
		label = fmt.Sprintf("Action%d", a.ID)
	}
	macro := fmt.Sprintf(`F1 %s,"%s fire %d"`, label, exe, a.ID)
	if err := walk.Clipboard().SetText(macro); err != nil {
		g.logf("Clipboard: %v", err)
		return
	}
	g.logf("Copied: %s   (change F1 to the key you want)", macro)
	if strings.Contains(exe, " ") {
		g.logf("WARNING: the exe path contains spaces; N1MM needs a path without spaces, e.g. C:\\Ham\\SliceTrigger.exe")
	}
}

func indexOf(list []string, s string, def int) int {
	for i, v := range list {
		if strings.EqualFold(v, s) {
			return i
		}
	}
	return def
}

var (
	sliceChoices = []string{"Active", "A", "B", "C", "D", "E", "F", "G", "H"}
	antChoices   = []string{"ANT1", "ANT2", "RX_A", "RX_B", "XVTA", "XVTB"}
	dvkOps       = []string{DVKPlay, DVKStop}
	dvkOpLabels  = []string{"Play memory", "Stop playback"}
	kmModeLabels = []string{"Relay ON", "Relay OFF", "Only this relay ON (others off)", "Bit pattern (hex)", "All OFF"}
)

// editAction shows the action dialog; only the fields for the selected type
// are visible.
func editAction(owner walk.Form, a Act, usedIDs map[int]bool, title string) (Act, bool) {
	var (
		dlg                                      *walk.Dialog
		okPB, cancelPB                           *walk.PushButton
		idEdit, relayEdit, timeoutEdit           *walk.NumberEdit
		nameEdit, cmdEdit, hostEdit, patternEdit *walk.LineEdit
		urlEdit, userEdit, passEdit, addrEdit    *walk.LineEdit
		dataEdit                                 *walk.LineEdit
		typeCB, sliceCB, antCB, modeCB           *walk.ComboBox
		replyCB, rrCB                            *walk.CheckBox
		antChecks                                = make([]*walk.CheckBox, len(antChoices))
		grpSlice, grpAnt, grpCmd, grpKM, grpURL  *walk.Composite
		grpAuth, grpTCP, grpDVK                  *walk.Composite
		dvkOpCB                                  *walk.ComboBox
		memEdit                                  *walk.NumberEdit
	)
	typeNames := make([]string, len(actionTypes))
	for i, t := range actionTypes {
		typeNames[i] = typeLabels[t]
	}
	grid := Grid{Columns: 2, MarginsZero: true}

	inCycle := map[string]bool{}
	for _, v := range splitList(a.Cycle) {
		inCycle[strings.ToUpper(v)] = true
	}
	antCheckWidgets := make([]Widget, len(antChoices))
	for i, name := range antChoices {
		antCheckWidgets[i] = CheckBox{AssignTo: &antChecks[i], Text: name, Checked: inCycle[name]}
	}
	updateCycle := func() {
		rr := rrCB.Checked()
		antCB.SetEnabled(!rr)
		for _, cb := range antChecks {
			cb.SetEnabled(rr)
		}
	}

	updateVisible := func() {
		t := actionTypes[typeCB.CurrentIndex()]
		grpSlice.SetVisible(t == TypeFlexRxAnt || t == TypeFlexRaw)
		grpAnt.SetVisible(t == TypeFlexRxAnt)
		grpCmd.SetVisible(t == TypeFlexRaw)
		grpKM.SetVisible(t == TypeKMtronic)
		grpURL.SetVisible(t == TypeHTTP)
		grpAuth.SetVisible(t == TypeKMtronic || t == TypeHTTP)
		grpTCP.SetVisible(t == TypeTCP)
		grpDVK.SetVisible(t == TypeFlexDVK)
		memEdit.SetEnabled(dvkOpCB.CurrentIndex() == 0)
	}

	collect := func() (Act, error) {
		n := Act{
			ID:   int(idEdit.Value()),
			Name: strings.TrimSpace(nameEdit.Text()),
			Type: actionTypes[typeCB.CurrentIndex()],
		}
		if usedIDs[n.ID] {
			return n, fmt.Errorf("ID %d is already used by another action.", n.ID)
		}
		if strings.Contains(n.Name, ",") || strings.Contains(n.Name, "/") {
			return n, fmt.Errorf("The name must not contain ',' or '/'.")
		}
		switch n.Type {
		case TypeFlexRxAnt:
			n.Slice = sliceChoices[sliceCB.CurrentIndex()]
			n.Antenna = strings.TrimSpace(antCB.Text())
			if rrCB.Checked() {
				var picked []string
				for i, cb := range antChecks {
					if cb.Checked() {
						picked = append(picked, antChoices[i])
					}
				}
				if len(picked) < 1 {
					return n, fmt.Errorf("Check at least one antenna to switch to.")
				}
				n.Cycle = strings.Join(picked, ",")
			} else if n.Antenna == "" {
				return n, fmt.Errorf("Choose an antenna.")
			}
		case TypeFlexRaw:
			n.Slice = sliceChoices[sliceCB.CurrentIndex()]
			n.Command = strings.TrimSpace(cmdEdit.Text())
			if n.Command == "" {
				return n, fmt.Errorf("Enter a command.")
			}
		case TypeKMtronic:
			n.Host = strings.TrimSpace(hostEdit.Text())
			n.Mode = kmModes[modeCB.CurrentIndex()]
			n.Relay = int(relayEdit.Value())
			n.Pattern = strings.TrimSpace(patternEdit.Text())
			n.User, n.Pass = userEdit.Text(), passEdit.Text()
			if _, err := kmtronicURL(n); err != nil {
				return n, err
			}
		case TypeHTTP:
			n.URL = strings.TrimSpace(urlEdit.Text())
			n.User, n.Pass = userEdit.Text(), passEdit.Text()
			if !strings.HasPrefix(n.URL, "http://") && !strings.HasPrefix(n.URL, "https://") {
				return n, fmt.Errorf("The URL must start with http:// or https://.")
			}
		case TypeFlexDVK:
			n.DVKOp = dvkOps[dvkOpCB.CurrentIndex()]
			if n.DVKOp == DVKPlay {
				n.Memory = int(memEdit.Value())
			}
		case TypeTCP:
			n.Addr = strings.TrimSpace(addrEdit.Text())
			n.Data = dataEdit.Text()
			n.ReadReply = replyCB.Checked()
			n.TimeoutMs = int(timeoutEdit.Value())
			if !strings.Contains(n.Addr, ":") {
				return n, fmt.Errorf("Enter the target as host:port.")
			}
			if _, err := unescape(n.Data); err != nil {
				return n, fmt.Errorf("Data: %v", err)
			}
		}
		return n, nil
	}

	var result Act
	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 460, Height: 300},
		Layout:        VBox{},
		Children: []Widget{
			Composite{Layout: grid, Children: []Widget{
				Label{Text: "ID:"},
				NumberEdit{AssignTo: &idEdit, Value: float64(a.ID), Decimals: 0, MinValue: 1, MaxValue: 9999},
				Label{Text: "Name:"},
				LineEdit{AssignTo: &nameEdit, Text: a.Name},
				Label{Text: "Type:"},
				ComboBox{AssignTo: &typeCB, Model: typeNames, CurrentIndex: indexOf(actionTypes, a.Type, 0),
					OnCurrentIndexChanged: func() {
						if grpTCP != nil {
							updateVisible()
						}
					}},
			}},
			Composite{AssignTo: &grpSlice, Layout: grid, Children: []Widget{
				Label{Text: "Slice:"},
				ComboBox{AssignTo: &sliceCB, Model: sliceChoices, CurrentIndex: indexOf(sliceChoices, a.Slice, 0)},
			}},
			Composite{AssignTo: &grpAnt, Layout: grid, Children: []Widget{
				Label{Text: "RX antenna:"},
				ComboBox{AssignTo: &antCB, Model: antChoices, Editable: true},
				Label{Text: ""},
				CheckBox{AssignTo: &rrCB, Text: "Cycle: each press moves to the next checked antenna, then back to the ANT1/ANT2 it started on",
					Checked: len(inCycle) > 0, OnCheckedChanged: func() {
						if antChecks[len(antChecks)-1] != nil {
							updateCycle()
						}
					}},
				Label{Text: ""},
				Composite{Layout: HBox{MarginsZero: true}, Children: antCheckWidgets},
			}},
			Composite{AssignTo: &grpCmd, Layout: grid, Children: []Widget{
				Label{Text: "Command(s):"},
				LineEdit{AssignTo: &cmdEdit, Text: a.Command,
					ToolTipText: "Separate several commands with ';'. {slice} is replaced by the slice index."},
				Label{Text: ""},
				Label{Text: "Example: slice set {slice} nb=1; slice set {slice} nr=0"},
			}},
			Composite{AssignTo: &grpDVK, Layout: grid, Children: []Widget{
				Label{Text: "Operation:"},
				ComboBox{AssignTo: &dvkOpCB, Model: dvkOpLabels, CurrentIndex: indexOf(dvkOps, a.DVKOp, 0),
					OnCurrentIndexChanged: func() {
						if memEdit != nil {
							memEdit.SetEnabled(dvkOpCB.CurrentIndex() == 0)
						}
					}},
				Label{Text: "Memory (1-12):"},
				NumberEdit{AssignTo: &memEdit, Value: float64(max(a.Memory, 1)), Decimals: 0, MinValue: 1, MaxValue: DVKSlots},
				Label{Text: ""},
				Label{Text: "Playing a memory keys the transmitter."},
			}},
			Composite{AssignTo: &grpKM, Layout: grid, Children: []Widget{
				Label{Text: "Relay board IP:"},
				LineEdit{AssignTo: &hostEdit, Text: a.Host},
				Label{Text: "Operation:"},
				ComboBox{AssignTo: &modeCB, Model: kmModeLabels, CurrentIndex: indexOf(kmModes, a.Mode, 0)},
				Label{Text: "Relay (1-8):"},
				NumberEdit{AssignTo: &relayEdit, Value: float64(max(a.Relay, 1)), Decimals: 0, MinValue: 1, MaxValue: 8},
				Label{Text: "Pattern (hex):"},
				LineEdit{AssignTo: &patternEdit, Text: a.Pattern, MaxLength: 2,
					ToolTipText: "Only for 'Bit pattern': hex byte, bit 0 = relay 1 (05 = relays 1 and 3)."},
			}},
			Composite{AssignTo: &grpURL, Layout: grid, Children: []Widget{
				Label{Text: "URL:"},
				LineEdit{AssignTo: &urlEdit, Text: a.URL},
			}},
			Composite{AssignTo: &grpAuth, Layout: grid, Children: []Widget{
				Label{Text: "User (optional):"},
				LineEdit{AssignTo: &userEdit, Text: a.User},
				Label{Text: "Password:"},
				LineEdit{AssignTo: &passEdit, Text: a.Pass, PasswordMode: true},
			}},
			Composite{AssignTo: &grpTCP, Layout: grid, Children: []Widget{
				Label{Text: "Host:port:"},
				LineEdit{AssignTo: &addrEdit, Text: a.Addr},
				Label{Text: "Data:"},
				LineEdit{AssignTo: &dataEdit, Text: a.Data, ToolTipText: `Escapes: \r \n \t \\ \xHH`},
				Label{Text: "Timeout (ms):"},
				NumberEdit{AssignTo: &timeoutEdit, Value: float64(max(a.TimeoutMs, 100)), Decimals: 0, MinValue: 100, MaxValue: 30000},
				Label{Text: ""},
				CheckBox{AssignTo: &replyCB, Text: "Read and log reply", Checked: a.ReadReply},
			}},
			VSpacer{},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okPB, Text: "OK", OnClicked: func() {
					n, err := collect()
					if err != nil {
						walk.MsgBox(dlg, title, err.Error(), walk.MsgBoxIconWarning)
						return
					}
					result = n
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancelPB, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(owner)
	if err != nil {
		walk.MsgBox(owner, title, err.Error(), walk.MsgBoxIconError)
		return a, false
	}
	antCB.SetText(orDefault(a.Antenna, "ANT1"))
	updateVisible()
	updateCycle()
	if dlg.Run() != walk.DlgCmdOK {
		return a, false
	}
	return result, true
}
