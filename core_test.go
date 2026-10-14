package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockRadio is a fake Flex radio on a local TCP port.
type mockRadio struct {
	ln net.Listener

	noDVK bool // behave like a radio without the voice keyer

	mu    sync.Mutex
	cmds  []string
	conns []net.Conn
}

const mockHandle = "1A2B3C4D"

var mockStatus = []string{
	"client 0xAAAA0001 connected local_ptt=1 program=SmartSDR-Win station=SHACK\x7fPC",
	"client 0xAAAA0002 connected program=SmartSDR-Win station=Remote",
	"slice 0 in_use=1 index_letter=A client_handle=0xAAAA0001 active=0 rxant=ANT1",
	"slice 1 in_use=1 index_letter=B client_handle=0xAAAA0001 rxant=ANT1",
	"slice 2 in_use=1 index_letter=A client_handle=0xAAAA0002 active=1",
	"slice 1 active=1", // partial update, must merge
}

func newMockRadio(t *testing.T) *mockRadio {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockRadio{ln: ln}
	t.Cleanup(func() { ln.Close(); m.dropAll() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			m.conns = append(m.conns, c)
			m.mu.Unlock()
			go m.serve(c)
		}
	}()
	return m
}

func (m *mockRadio) serve(c net.Conn) {
	fmt.Fprintf(c, "V1.4.0.0\nH%s\n", mockHandle)
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		line := sc.Text()
		seq, cmd, ok := strings.Cut(strings.TrimPrefix(line, "C"), "|")
		if !ok {
			continue
		}
		m.mu.Lock()
		m.cmds = append(m.cmds, cmd)
		m.mu.Unlock()
		switch {
		case cmd == "sub slice all":
			fmt.Fprintf(c, "R%s|0|\n", seq)
			for _, s := range mockStatus {
				fmt.Fprintf(c, "S%s|%s\n", mockHandle, s)
			}
		case cmd == "info":
			fmt.Fprintf(c, "R%s|0|model=\"FLEX-8600\",chassis_serial=\"1234-5678\",name=\"Contest\",callsign=\"VA3MW\",software_ver=4.1.3.39644\n", seq)
		case cmd == "sub dvk all" && m.noDVK:
			fmt.Fprintf(c, "R%s|50000015|unknown command\n", seq)
		case cmd == "sub dvk all":
			fmt.Fprintf(c, "R%s|0|\n", seq)
			fmt.Fprintf(c, "S%s|dvk added id=1 name=\"CQ contest\" duration=2720\n", mockHandle)
			fmt.Fprintf(c, "S%s|dvk status=idle enabled=1\n", mockHandle)
		case strings.HasPrefix(cmd, "dvk playback_start id="):
			// Like the radio: status before the reply.
			fmt.Fprintf(c, "S%s|dvk status=playback id=%s enabled=1\n", mockHandle, strings.TrimPrefix(cmd, "dvk playback_start id="))
			fmt.Fprintf(c, "R%s|0|\n", seq)
		case cmd == "dvk playback_stop" || cmd == "dvk preview_stop":
			fmt.Fprintf(c, "S%s|dvk status=idle enabled=1\n", mockHandle)
			fmt.Fprintf(c, "R%s|0|\n", seq)
		case strings.HasPrefix(cmd, "bad"):
			fmt.Fprintf(c, "R%s|50000015|unknown command\n", seq)
		default:
			fmt.Fprintf(c, "R%s|0|\n", seq)
		}
	}
}

func (m *mockRadio) addr() string { return m.ln.Addr().String() }

func (m *mockRadio) dropAll() {
	m.mu.Lock()
	for _, c := range m.conns {
		c.Close()
	}
	m.conns = nil
	m.mu.Unlock()
}

func (m *mockRadio) saw(cmd string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cmds {
		if c == cmd {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func connectFlex(t *testing.T, f *Flex, m *mockRadio) {
	t.Helper()
	f.RetryDelay = 20 * time.Millisecond
	f.Connect(m.addr())
	t.Cleanup(f.Disconnect)
	waitSynced(t, f)
}

// waitSynced waits until the partial "slice 1 active=1" update has landed.
func waitSynced(t *testing.T, f *Flex) {
	t.Helper()
	waitFor(t, "slice status", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.slices[1]["active"] == "1"
	})
}

func TestFlexStatusMergeAndResolve(t *testing.T) {
	m := newMockRadio(t)
	f := NewFlex()
	connectFlex(t, f, m)

	f.mu.Lock()
	s1 := f.slices[1]
	if s1["index_letter"] != "B" || s1["rxant"] != "ANT1" || s1["client_handle"] != "0xAAAA0001" {
		t.Errorf("partial update lost earlier keys: %v", s1)
	}
	f.mu.Unlock()

	cases := []struct {
		sel, station string
		want         int
		wantErr      bool
	}{
		{"B", "", 1, false},
		{"b", "", 1, false},
		{"A", "", 0, false}, // two A slices, lowest index without a station
		{"A", "Remote", 2, false},
		{"A", "shack pc", 0, false}, // 0x7F decoded, case-insensitive
		{"Active", "SHACK PC", 1, false},
		{"Active", "Remote", 2, false},
		{"", "", 0, true},       // two clients each have an active slice
		{"Active", "", 0, true}, // ambiguous without a station
		{"C", "", 0, true},
		{"B", "Remote", 0, true},
		{"A", "Nobody", 0, true},
	}
	for _, c := range cases {
		got, err := f.Resolve(c.sel, c.station)
		if (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
			t.Errorf("Resolve(%q,%q) = %d, %v; want %d, err=%v", c.sel, c.station, got, err, c.want, c.wantErr)
		}
	}

	// Active follows status updates and ignores TX focus: TX is on slice 0,
	// the active flag moves from 1 to 0.
	f.handleStatus("slice 0 tx=1")
	f.handleStatus("slice 1 tx=0")
	if idx, err := f.Resolve("Active", "SHACK PC"); err != nil || idx != 1 {
		t.Errorf("active must not follow tx: %d, %v", idx, err)
	}
	f.handleStatus("slice 1 active=0")
	f.handleStatus("slice 0 active=1")
	if idx, err := f.Resolve("Active", "SHACK PC"); err != nil || idx != 0 {
		t.Errorf("active did not follow status: %d, %v", idx, err)
	}

	// Slice removal and client disconnect.
	f.handleStatus("slice 2 in_use=0")
	if idx, err := f.Resolve("Active", ""); err != nil || idx != 0 {
		t.Errorf("single active slice without station: %d, %v", idx, err)
	}
	if _, err := f.Resolve("A", "Remote"); err == nil {
		t.Error("removed slice still resolves")
	}
	f.handleStatus("client 0xAAAA0002 disconnected forced=0")
	if _, err := f.Resolve("Active", "Remote"); err == nil || !strings.Contains(err.Error(), "station") {
		t.Errorf("disconnected station: %v", err)
	}
}

func TestFlexCommandReplies(t *testing.T) {
	m := newMockRadio(t)
	f := NewFlex()
	connectFlex(t, f, m)

	if _, err := f.Command("info", time.Second); err != nil {
		t.Fatal(err)
	}
	rep, err := f.Command("bad thing", time.Second)
	if err == nil || rep.Code != 0x50000015 || rep.Msg != "unknown command" {
		t.Errorf("error reply: %+v, %v", rep, err)
	}
	if !m.saw("sub slice all") || !m.saw("sub client all") {
		t.Error("subscriptions not sent")
	}
}

func TestFlexReconnect(t *testing.T) {
	m := newMockRadio(t)
	f := NewFlex()
	connectFlex(t, f, m)

	m.dropAll()
	waitFor(t, "disconnect", func() bool { return !f.Connected() })
	if _, err := f.Command("info", time.Second); err == nil {
		t.Error("command succeeded while disconnected")
	}
	waitSynced(t, f)
	if idx, err := f.Resolve("B", ""); err != nil || idx != 1 {
		t.Errorf("after reconnect: %d, %v", idx, err)
	}
}

func TestRadioInfo(t *testing.T) {
	m := newMockRadio(t)
	f := NewFlex()
	var mu sync.Mutex
	var states []string
	f.OnState = func(connected bool, text string) {
		mu.Lock()
		states = append(states, fmt.Sprint(connected, " ", text))
		mu.Unlock()
	}
	connectFlex(t, f, m)
	waitFor(t, "radio label", func() bool { return f.RadioLabel() == "Contest (FLEX-8600)" })
	waitFor(t, "state update", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(states) > 0 && states[len(states)-1] == "true Contest (FLEX-8600)"
	})
	if f.RadioName() != "Contest" {
		t.Errorf("name %q", f.RadioName())
	}
	info := parseInfo(`model="FLEX-6600",name="My\ Radio",software_ver=3.5.9`)
	if info["model"] != "FLEX-6600" || info["name"] != "My Radio" || info["software_ver"] != "3.5.9" {
		t.Errorf("parseInfo: %v", info)
	}
}

func TestFlexNotConnected(t *testing.T) {
	f := NewFlex()
	if _, err := f.Resolve("A", ""); err == nil {
		t.Error("Resolve without connection")
	}
	if _, err := f.Command("info", time.Second); err == nil {
		t.Error("Command without connection")
	}
}

func TestKMtronicURL(t *testing.T) {
	cases := []struct {
		a    Act
		want string
	}{
		{Act{Host: "192.168.1.199", Mode: KMOn, Relay: 1}, "http://192.168.1.199/FF0101"},
		{Act{Host: "192.168.1.199", Mode: KMOff, Relay: 1}, "http://192.168.1.199/FF0100"},
		{Act{Host: "10.0.0.5:8080", Mode: KMOn, Relay: 8}, "http://10.0.0.5:8080/FF0801"},
		{Act{Host: "h", Mode: KMOnly, Relay: 1}, "http://h/FFE001"},
		{Act{Host: "h", Mode: KMOnly, Relay: 3}, "http://h/FFE004"},
		{Act{Host: "h", Mode: KMOnly, Relay: 8}, "http://h/FFE080"},
		{Act{Host: "h", Mode: KMPattern, Pattern: "a5"}, "http://h/FFE0A5"},
		{Act{Host: "h", Mode: KMAllOff}, "http://h/FFE000"},
	}
	for _, c := range cases {
		if got, err := kmtronicURL(c.a); err != nil || got != c.want {
			t.Errorf("%+v: %q, %v; want %q", c.a, got, err, c.want)
		}
	}
	for _, bad := range []Act{
		{Mode: KMOn, Relay: 1},
		{Host: "h", Mode: KMOn, Relay: 0},
		{Host: "h", Mode: KMOnly, Relay: 9},
		{Host: "h", Mode: KMPattern, Pattern: "1FF"},
		{Host: "h", Mode: "toggle", Relay: 1},
	} {
		if u, err := kmtronicURL(bad); err == nil {
			t.Errorf("%+v: expected error, got %q", bad, u)
		}
	}
}

func TestNextRxAnt(t *testing.T) {
	rx := []string{"RX_A", "RX_B"}
	all := []string{"ANT1", "ANT2", "RX_A", "RX_B", "XVTA", "XVTB"}
	cases := []struct {
		ticked    []string
		cur, home string
		avail     []string
		want      string
	}{
		{rx, "ANT1", "ANT1", nil, "RX_A"},
		{rx, "RX_A", "ANT1", nil, "RX_B"},
		{rx, "RX_B", "ANT1", nil, "ANT1"}, // back home
		{rx, "RX_B", "ANT2", nil, "ANT2"},
		{rx, "XVTA", "ANT1", nil, "ANT1"},  // not part of the cycle: go home
		{all, "ANT1", "ANT1", nil, "ANT2"}, // the other main antenna is an option
		{all, "XVTB", "ANT1", nil, "ANT1"},
		{all, "ANT2", "ANT2", nil, "ANT1"},
		{all, "ANT1", "ANT2", nil, "RX_A"},
		{rx, "ANT1", "ANT1", []string{"ANT1", "ANT2", "RX_A"}, "RX_A"},
		{rx, "RX_A", "ANT1", []string{"ANT1", "ANT2", "RX_A"}, "ANT1"}, // RX_B not offered
		{[]string{"ANT1"}, "ANT1", "ANT1", nil, "ANT1"},                // nothing to switch to
	}
	for _, c := range cases {
		if got := nextRxAnt(c.ticked, c.cur, c.home, c.avail); got != c.want {
			t.Errorf("nextRxAnt(%v, %q, home %q, %v) = %q, want %q", c.ticked, c.cur, c.home, c.avail, got, c.want)
		}
	}
}

func (m *mockRadio) rxants(idx int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	prefix := fmt.Sprintf("slice set %d rxant=", idx)
	for _, c := range m.cmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, strings.TrimPrefix(c, prefix))
		}
	}
	return out
}

func TestRxAntCycle(t *testing.T) {
	m := newMockRadio(t)
	cfg := &Config{Station: "SHACK PC", Actions: []Act{
		{ID: 6, Name: "RxAnt", Type: TypeFlexRxAnt, Slice: "Active", Cycle: "RX_A, RX_B"},
	}}
	b := NewBridge(cfg, "")
	connectFlex(t, b.Flex, m)
	fire := func() {
		t.Helper()
		if res := b.Fire("6"); len(res) != 1 || !res[0].OK {
			t.Fatalf("fire: %v", res)
		}
	}

	// Active slice 1 is on ANT1: ANT1 > RX_A > RX_B > ANT1 > RX_A. The mock
	// sends no status after a set, so stepping relies on the recorded value.
	for i := 0; i < 4; i++ {
		fire()
	}
	// Operator moves the slice to ANT2 by hand: ANT2 becomes home.
	b.Flex.handleStatus("slice 1 rxant=ANT2")
	fire()
	fire()
	fire()
	// Operator picks RX_B by hand (not a main antenna): home stays ANT2.
	b.Flex.handleStatus("slice 1 rxant=RX_B")
	fire()
	want1 := "RX_A RX_B ANT1 RX_A RX_A RX_B ANT2 ANT2"
	if got := strings.Join(m.rxants(1), " "); got != want1 {
		t.Errorf("slice 1 sequence %q, want %q", got, want1)
	}

	// A slice already on RX_A with no history returns to its TX antenna.
	b.Flex.handleStatus("slice 0 rxant=RX_A txant=ANT2")
	b.Flex.handleStatus("slice 1 active=0")
	b.Flex.handleStatus("slice 0 active=1")
	fire()
	fire()
	if got := strings.Join(m.rxants(0), " "); got != "RX_B ANT2" {
		t.Errorf("slice 0 sequence %q, want \"RX_B ANT2\"", got)
	}
}

func (m *mockRadio) count(cmd string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.cmds {
		if c == cmd {
			n++
		}
	}
	return n
}

func TestVoiceKeyer(t *testing.T) {
	m := newMockRadio(t)
	cfg := &Config{Actions: []Act{
		{ID: 1, Name: "CQ", Type: TypeFlexDVK, DVKOp: DVKPlay, Memory: 3},
		{ID: 2, Name: "Stop", Type: TypeFlexDVK, DVKOp: DVKStop},
		{ID: 3, Name: "Bad", Type: TypeFlexDVK, DVKOp: DVKPlay, Memory: 13},
	}}
	b := NewBridge(cfg, "")
	connectFlex(t, b.Flex, m)
	waitFor(t, "dvk status", func() bool { return b.Flex.DVK().Known })

	// ESC while idle sends nothing.
	if msg, err := b.StopDVK(true); msg != "" || err != nil {
		t.Errorf("ESC while idle: %q, %v", msg, err)
	}

	if res := b.Fire("CQ"); !res[0].OK || !m.saw("dvk playback_start id=3") {
		t.Fatalf("play: %v", res)
	}
	if st := b.Flex.DVK(); st.Status != "playback" || st.ID != 3 {
		t.Errorf("state after play: %+v", st)
	}

	// ESC while playing stops it.
	if msg, err := b.StopDVK(true); msg != "dvk playback_stop" || err != nil {
		t.Errorf("ESC while playing: %q, %v", msg, err)
	}
	waitFor(t, "idle", func() bool { return b.Flex.DVK().Status == "idle" })

	// Stop action while idle: no command, reported OK.
	if res := b.Fire("2"); !res[0].OK || m.count("dvk playback_stop") != 1 {
		t.Errorf("stop while idle: %v, stops=%d", res, m.count("dvk playback_stop"))
	}

	// Preview (local listen) is stopped with preview_stop; recording is left alone.
	b.Flex.handleStatus("dvk status=preview id=5 enabled=1")
	if msg, _ := b.StopDVK(true); msg != "dvk preview_stop" {
		t.Errorf("ESC during preview: %q", msg)
	}
	b.Flex.handleStatus("dvk status=recording id=5 enabled=1")
	if msg, _ := b.StopDVK(true); msg != "" {
		t.Errorf("ESC during recording: %q", msg)
	}

	if res := b.Fire("3"); res[0].OK {
		t.Error("memory 13 accepted")
	}
	b.Flex.handleStatus("dvk status=disabled enabled=0")
	if res := b.Fire("1"); res[0].OK || !strings.Contains(res[0].Msg, "disabled") {
		t.Errorf("play while disabled: %v", res)
	}
}

func TestVoiceKeyerWithoutStatus(t *testing.T) {
	// A radio that rejects "sub dvk all": ESC only stops what we started.
	m := newMockRadio(t)
	m.noDVK = true
	b := NewBridge(&Config{}, "")
	connectFlex(t, b.Flex, m)

	if msg, _ := b.StopDVK(true); msg != "" {
		t.Errorf("ESC before any play: %q", msg)
	}
	if _, err := b.PlayDVK(2); err != nil {
		t.Fatal(err)
	}
	// The mock still sends a playback status line after the start; drop it to
	// simulate a radio that reports nothing.
	b.Flex.mu.Lock()
	b.Flex.dvk = DVKState{}
	b.Flex.mu.Unlock()
	if msg, err := b.StopDVK(true); msg != "dvk playback_stop" || err != nil {
		t.Errorf("ESC after our play: %q, %v", msg, err)
	}
	if msg, _ := b.StopDVK(true); msg != "" {
		t.Errorf("second ESC: %q", msg)
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.8.1", "0.8.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0", "0.99.99", true},
		{"0.8.0", "0.8.0", false},
		{"0.8", "0.8.0", false},
		{"0.7.9", "0.8.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestUpdate(t *testing.T) {
	exe := make([]byte, 2<<20)
	copy(exe, "MZ new build")
	sum := sha256.Sum256(exe)
	version := "9.9.9\n" + hex.EncodeToString(sum[:]) + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/VERSION":
			io.WriteString(w, version)
		case "/SliceTrigger.exe":
			w.Write(exe)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/"

	info, err := fetchLatest(base)
	if err != nil || info.Version != "9.9.9" || !newerVersion(info.Version, Version) {
		t.Fatalf("fetchLatest: %+v, %v", info, err)
	}

	dir := t.TempDir()
	cur := filepath.Join(dir, "SliceTrigger.exe")
	os.WriteFile(cur, []byte("MZ old build"), 0o755)
	newPath, err := downloadUpdate(base, info, cur)
	if err != nil {
		t.Fatal(err)
	}
	if err := installUpdate(cur, newPath); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cur); !bytes.Equal(got, exe) {
		t.Error("exe not replaced")
	}
	if got, _ := os.ReadFile(cur + ".old"); string(got) != "MZ old build" {
		t.Error("old exe not kept")
	}
	cleanupOldExe(cur)
	if _, err := os.Stat(cur + ".old"); err == nil {
		t.Error(".old not removed")
	}

	// A checksum mismatch leaves nothing behind.
	bad := info
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := downloadUpdate(base, bad, cur); err == nil {
		t.Error("bad checksum accepted")
	}
	if _, err := os.Stat(cur + ".new"); err == nil {
		t.Error(".new left after failed download")
	}
	version = "garbage\n"
	if _, err := fetchLatest(base); err == nil {
		t.Error("malformed VERSION accepted")
	}
}

func TestUnescape(t *testing.T) {
	got, err := unescape(`AB\r\n\x00\xfF\t\\x`)
	want := "AB\r\n\x00\xff\t\\x"
	if err != nil || string(got) != want {
		t.Errorf("got %q, %v", got, err)
	}
	for _, bad := range []string{`a\`, `\x1`, `\xZZ`, `\q`} {
		if _, err := unescape(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestParseDiscovery(t *testing.T) {
	pkt := append([]byte{0x38, 0x52, 0x00, 0x51, 0, 0, 0x08, 0, 0, 0, 0x1c, 0x2d, 0x53, 0x4c, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		[]byte("discovery_protocol_version=3.0.0.2 model=FLEX-6600 serial=1234-5678-6600-0001 version=3.5.9.1 nickname=Contest callsign=K1ABC ip=192.168.1.50 port=4992 status=Available\x00\x00")...)
	r, ok := parseDiscovery(pkt)
	if !ok || r.IP != "192.168.1.50" || r.Model != "FLEX-6600" || r.Nickname != "Contest" || r.Serial != "1234-5678-6600-0001" {
		t.Errorf("got %+v, %v", r, ok)
	}
	if _, ok := parseDiscovery([]byte("hello world")); ok {
		t.Error("junk parsed as radio")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", configName)
	c, err := LoadConfig(path)
	if err != nil || c.Port != 8765 {
		t.Fatalf("defaults: %+v, %v", c, err)
	}
	c.RadioIP, c.Station, c.Port = "192.168.1.50", "SHACK", 9000
	c.Actions = []Act{{ID: 3, Name: "Ant2", Type: TypeFlexRxAnt, Slice: "A", Antenna: "ANT2"}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfig(path)
	if err != nil || c2.Port != 9000 || len(c2.Actions) != 1 || c2.Actions[0] != c.Actions[0] {
		t.Errorf("reload: %+v, %v", c2, err)
	}
}

// TestTriggerEndToEnd drives every action type through the HTTP trigger.
func TestTriggerEndToEnd(t *testing.T) {
	m := newMockRadio(t)

	var hmu sync.Mutex
	var hits []string
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if strings.HasPrefix(r.URL.Path, "/secret") && (u != "admin" || p != "admin") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		hmu.Lock()
		hits = append(hits, r.URL.Path+"|"+u)
		hmu.Unlock()
	}))
	defer web.Close()
	webHost := strings.TrimPrefix(web.URL, "http://")

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpLn.Close()
	tcpGot := make(chan string, 1)
	go func() {
		c, err := tcpLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		tcpGot <- string(buf[:n])
		io.WriteString(c, "OK\r")
	}()

	cfg := &Config{Station: "SHACK PC", Actions: []Act{
		{ID: 1, Name: "Ant2", Type: TypeFlexRxAnt, Slice: "B", Antenna: "ANT2"},
		{ID: 2, Name: "RxA active", Type: TypeFlexRxAnt, Slice: "Active", Antenna: "RX_A"},
		{ID: 3, Name: "Raw", Type: TypeFlexRaw, Slice: "A", Command: "slice set {slice} nb=1; info"},
		{ID: 4, Name: "Relay", Type: TypeKMtronic, Host: webHost, Mode: KMOnly, Relay: 2, User: "admin", Pass: "admin"},
		{ID: 5, Name: "Get", Type: TypeHTTP, URL: web.URL + "/secret/go", User: "admin", Pass: "admin"},
		{ID: 6, Name: "NoAuth", Type: TypeHTTP, URL: web.URL + "/secret/go"},
		{ID: 7, Name: "Tcp", Type: TypeTCP, Addr: tcpLn.Addr().String(), Data: `AN2;\r`, ReadReply: true},
		{ID: 8, Name: "BadRaw", Type: TypeFlexRaw, Command: "bad one; info"},
	}}
	b := NewBridge(cfg, "")
	connectFlex(t, b.Flex, m)
	srv, err := StartTrigger(0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	out, err := FireClient(srv.Port(), "1,2")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !m.saw("slice set 1 rxant=ANT2") || !m.saw("slice set 1 rxant=RX_A") {
		t.Errorf("rxant commands missing: %v", m.cmds)
	}

	if out, err := FireClient(srv.Port(), "raw"); err != nil {
		t.Fatalf("by name: %v: %s", err, out)
	}
	if !m.saw("slice set 0 nb=1") || !m.saw("info") {
		t.Errorf("raw commands missing: %v", m.cmds)
	}

	if out, err := FireClient(srv.Port(), "4,Get"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	hmu.Lock()
	if len(hits) != 2 || hits[0] != "/FFE002|admin" || hits[1] != "/secret/go|admin" {
		t.Errorf("http hits: %v", hits)
	}
	hmu.Unlock()

	out, err = FireClient(srv.Port(), "RxA active, 7")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := <-tcpGot; got != "AN2;\r" {
		t.Errorf("tcp payload %q", got)
	}
	if !strings.Contains(out, `reply "OK\r"`) {
		t.Errorf("tcp reply not reported: %s", out)
	}

	// Failures: rejected auth, radio error (stops the chain), unknown action.
	if out, err := FireClient(srv.Port(), "6"); err == nil || !strings.Contains(out, "ERR 6 NoAuth") {
		t.Errorf("401 not reported: %v: %s", err, out)
	}
	if out, err := FireClient(srv.Port(), "8"); err == nil || !strings.Contains(out, "50000015") {
		t.Errorf("radio error not reported: %v: %s", err, out)
	}
	if out, err := FireClient(srv.Port(), "99"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("unknown action: %v: %s", err, out)
	}
}
