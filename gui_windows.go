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
	portEdit    *walk.NumberEdit
	tv          *walk.TableView
	logEdit     *walk.TextEdit

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

	err := MainWindow{
		AssignTo: &g.mw,
		Title:    "SliceTrigger",
		MinSize:  Size{Width: 640, Height: 420},
		Size:     Size{Width: 860, Height: 560},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "Radio IP:"},
					LineEdit{AssignTo: &g.ipEdit, Text: cfg.RadioIP, MinSize: Size{Width: 110}},
					PushButton{Text: "Discover", OnClicked: g.onDiscover},
					Label{Text: "Station:"},
					LineEdit{AssignTo: &g.stationEdit, Text: cfg.Station, MinSize: Size{Width: 100},
						ToolTipText: "Optional SmartSDR station name (multiFLEX). Empty = any client."},
					PushButton{AssignTo: &g.connectBtn, Text: "Connect", OnClicked: g.onConnect},
					Label{AssignTo: &g.statusLbl, Text: "Not connected", MinSize: Size{Width: 200}},
					HSpacer{},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "Trigger port:"},
					NumberEdit{AssignTo: &g.portEdit, Value: float64(cfg.Port), Decimals: 0, MinValue: 1, MaxValue: 65535, MinSize: Size{Width: 70}},
					PushButton{Text: "Apply", OnClicked: g.onApplyPort},
					HSpacer{},
				},
			},
			TableView{
				AssignTo:         &g.tv,
				AlternatingRowBG: true,
				Columns: []TableViewColumn{
					{Title: "ID", Width: 45},
					{Title: "Name", Width: 160},
					{Title: "Type", Width: 130},
					{Title: "Summary", Width: 440},
				},
				Model:           g.model,
				OnItemActivated: g.onEdit,
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Add", OnClicked: g.onAdd},
					PushButton{Text: "Edit", OnClicked: g.onEdit},
					PushButton{Text: "Duplicate", OnClicked: g.onDuplicate},
					PushButton{Text: "Delete", OnClicked: g.onDelete},
					PushButton{Text: "Test", OnClicked: g.onTest},
					PushButton{Text: "Copy N1MM macro", OnClicked: g.onCopyMacro},
					HSpacer{},
				},
			},
			TextEdit{AssignTo: &g.logEdit, ReadOnly: true, VScroll: true, MinSize: Size{Height: 110}},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, "SliceTrigger", "Cannot create window: "+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	g.logf("Config: %s", path)
	if cfgErr != nil {
		g.logf("Config could not be read, using defaults: %v", cfgErr)
	}
	g.startTrigger(cfg.Port)
	if cfg.AutoConnect && cfg.RadioIP != "" {
		g.connect()
	}
	g.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		g.b.Flex.OnState = nil
		g.b.Flex.Disconnect()
		if g.trigger != nil {
			g.trigger.Close()
		}
	})
	g.mw.Run()
}

func (g *gui) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, args...) + "\r\n"
	if g.mw == nil {
		return
	}
	g.mw.Synchronize(func() {
		if g.logEdit.TextLength() > 200000 {
			g.logEdit.SetText("")
		}
		g.logEdit.AppendText(line)
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

func (g *gui) onApplyPort() {
	port := int(g.portEdit.Value())
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
		ID: g.nextID(), Type: TypeFlexRxAnt, Slice: "Active", Antenna: "ANT1",
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
	id := g.model.items[i].ID
	go g.b.Fire(strconv.Itoa(id))
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
		grpAuth, grpTCP                          *walk.Composite
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
				if len(picked) < 2 {
					return n, fmt.Errorf("Round robin needs at least two antennas checked.")
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
				CheckBox{AssignTo: &rrCB, Text: "Round robin: each press selects the next checked antenna",
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
