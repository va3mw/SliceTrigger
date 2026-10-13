package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Flex is a minimal SmartSDR TCP API client (port 4992).
//
// Protocol as implemented here:
//
//	radio -> "V<version>" then "H<handle>" on connect
//	us    -> "C<seq>|<command>\n"
//	radio -> "R<seq>|<hex code>|<message>"   (code 0 = success)
//	radio -> "S<handle>|<object> <key=value ...>"  (partial status updates)
type Flex struct {
	Logf       func(format string, args ...any)
	OnState    func(connected bool, text string)
	RetryDelay time.Duration
	PingEvery  time.Duration

	mu      sync.Mutex
	conn    net.Conn
	ready   bool
	version string
	handle  string
	seq     int
	pending map[int]chan FlexReply
	slices  map[int]map[string]string
	clients map[uint32]map[string]string
	stop    chan struct{}
}

type FlexReply struct {
	Code uint32
	Msg  string
}

var errFlexTimeout = errors.New("timeout waiting for radio reply")

func NewFlex() *Flex {
	return &Flex{
		RetryDelay: 3 * time.Second,
		PingEvery:  10 * time.Second,
		pending:    map[int]chan FlexReply{},
		slices:     map[int]map[string]string{},
		clients:    map[uint32]map[string]string{},
	}
}

func (f *Flex) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(format, args...)
	}
}

// Connect starts (or restarts) the connection loop. It keeps reconnecting
// until Disconnect is called.
func (f *Flex) Connect(addr string) {
	f.Disconnect()
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "4992")
	}
	stop := make(chan struct{})
	f.mu.Lock()
	f.stop = stop
	f.mu.Unlock()
	go f.run(addr, stop)
}

func (f *Flex) Disconnect() {
	f.mu.Lock()
	if f.stop != nil {
		close(f.stop)
		f.stop = nil
	}
	if f.conn != nil {
		f.conn.Close()
	}
	f.mu.Unlock()
}

func (f *Flex) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

// state reports a state change unless this loop has been superseded.
func (f *Flex) state(stop chan struct{}, connected bool, text string) {
	f.mu.Lock()
	current := f.stop == stop
	f.mu.Unlock()
	if current && f.OnState != nil {
		f.OnState(connected, text)
	}
}

func (f *Flex) run(addr string, stop chan struct{}) {
	for {
		f.state(stop, false, "Connecting to "+addr+"...")
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			f.mu.Lock()
			select {
			case <-stop:
				f.mu.Unlock()
				conn.Close()
				return
			default:
			}
			f.conn = conn
			f.mu.Unlock()
			err = f.session(conn, stop)
		}
		select {
		case <-stop:
			if f.OnState != nil {
				f.OnState(false, "Not connected")
			}
			return
		default:
		}
		f.logf("Flex: %v; retrying in %v", err, f.RetryDelay)
		f.state(stop, false, "Disconnected, retrying...")
		select {
		case <-stop:
			return
		case <-time.After(f.RetryDelay):
		}
	}
}

func (f *Flex) session(conn net.Conn, stop chan struct{}) error {
	done := make(chan struct{})
	defer func() {
		close(done)
		conn.Close()
		f.mu.Lock()
		f.conn = nil
		f.ready = false
		for seq, ch := range f.pending {
			close(ch)
			delete(f.pending, seq)
		}
		f.slices = map[int]map[string]string{}
		f.clients = map[uint32]map[string]string{}
		f.mu.Unlock()
	}()

	r := bufio.NewReaderSize(conn, 64*1024)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("connection lost (%v)", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		switch line[0] {
		case 'V':
			f.mu.Lock()
			f.version = line[1:]
			f.mu.Unlock()
		case 'H':
			f.mu.Lock()
			f.handle = line[1:]
			f.ready = true
			version := f.version
			f.mu.Unlock()
			f.logf("Flex: connected, version %s, handle %s", version, line[1:])
			f.state(stop, true, "Connected (v"+version+")")
			go f.afterHandshake(conn, done)
		case 'R':
			f.handleReply(line[1:])
		case 'S':
			if i := strings.IndexByte(line, '|'); i >= 0 {
				f.handleStatus(line[i+1:])
			}
		}
	}
}

func (f *Flex) afterHandshake(conn net.Conn, done chan struct{}) {
	for _, cmd := range []string{"sub client all", "sub slice all"} {
		if _, err := f.Command(cmd, 5*time.Second); err != nil {
			f.logf("Flex: %q failed: %v", cmd, err)
		}
	}
	// Keepalive: only a missing reply counts as a dead link, an error reply
	// still proves the radio is there.
	t := time.NewTicker(f.PingEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if _, err := f.Command("ping", 5*time.Second); errors.Is(err, errFlexTimeout) {
				f.logf("Flex: keepalive timed out")
				conn.Close()
				return
			}
		}
	}
}

func (f *Flex) handleReply(s string) {
	parts := strings.SplitN(s, "|", 3)
	if len(parts) < 2 {
		return
	}
	seq, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	code, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 16, 32)
	if err != nil {
		code = 0xFFFFFFFF
	}
	rep := FlexReply{Code: uint32(code)}
	if len(parts) == 3 {
		rep.Msg = parts[2]
	}
	f.mu.Lock()
	ch := f.pending[seq]
	delete(f.pending, seq)
	f.mu.Unlock()
	if ch != nil {
		ch <- rep
	}
}

func parseHandle(s string) (uint32, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	v, err := strconv.ParseUint(s, 16, 32)
	return uint32(v), err == nil
}

func mergeKV(dst map[string]string, fields []string) {
	for _, fld := range fields {
		if k, v, ok := strings.Cut(fld, "="); ok {
			dst[k] = v
		}
	}
}

// handleStatus merges a partial status update into the slice/client tables.
func (f *Flex) handleStatus(body string) {
	fields := strings.Fields(body)
	if len(fields) < 2 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch fields[0] {
	case "slice":
		idx, err := strconv.Atoi(fields[1])
		if err != nil {
			return
		}
		m := f.slices[idx]
		if m == nil {
			m = map[string]string{}
			f.slices[idx] = m
		}
		mergeKV(m, fields[2:])
		if m["in_use"] == "0" {
			delete(f.slices, idx)
		}
	case "client":
		h, ok := parseHandle(fields[1])
		if !ok {
			return
		}
		if len(fields) > 2 && fields[2] == "disconnected" {
			delete(f.clients, h)
			return
		}
		m := f.clients[h]
		if m == nil {
			m = map[string]string{}
			f.clients[h] = m
		}
		mergeKV(m, fields[2:])
	}
}

// Command sends one API command and waits for its reply. A non-zero reply
// code is returned as an error (the reply is still returned).
func (f *Flex) Command(cmd string, timeout time.Duration) (FlexReply, error) {
	f.mu.Lock()
	if f.conn == nil || !f.ready {
		f.mu.Unlock()
		return FlexReply{}, errors.New("radio not connected")
	}
	f.seq++
	seq := f.seq
	ch := make(chan FlexReply, 1)
	f.pending[seq] = ch
	f.conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err := fmt.Fprintf(f.conn, "C%d|%s\n", seq, cmd)
	if err != nil {
		delete(f.pending, seq)
	}
	f.mu.Unlock()
	if err != nil {
		return FlexReply{}, err
	}
	select {
	case rep, ok := <-ch:
		if !ok {
			return FlexReply{}, errors.New("connection lost before reply")
		}
		if rep.Code != 0 {
			return rep, fmt.Errorf("radio error 0x%08X %s", rep.Code, rep.Msg)
		}
		return rep, nil
	case <-time.After(timeout):
		f.mu.Lock()
		delete(f.pending, seq)
		f.mu.Unlock()
		return FlexReply{}, errFlexTimeout
	}
}

// stationName decodes the radio's 0x7F-for-space encoding.
func stationName(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\x7f", " "))
}

// Resolve maps a slice letter (A..H) or "Active" to a slice index. A
// non-empty station restricts the search to slices owned by that client.
// "Active" means the slice reporting active=1 in its status (the one selected
// in SmartSDR), never the TX slice (tx=1). If several clients each have an
// active slice and no station is given, that is an error rather than a guess.
// For letters without a station filter the lowest matching index wins.
func (f *Flex) Resolve(sel, station string) (int, error) {
	sel = strings.TrimSpace(sel)
	station = strings.TrimSpace(station)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.ready {
		return 0, errors.New("radio not connected")
	}

	var owners map[uint32]bool
	if station != "" {
		owners = map[uint32]bool{}
		for h, c := range f.clients {
			if strings.EqualFold(stationName(c["station"]), station) {
				owners[h] = true
			}
		}
		if len(owners) == 0 {
			return 0, fmt.Errorf("station %q not found on radio", station)
		}
	}

	idxs := make([]int, 0, len(f.slices))
	for idx := range f.slices {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	active := sel == "" || strings.EqualFold(sel, "active")
	var actives []int
	for _, idx := range idxs {
		s := f.slices[idx]
		if owners != nil {
			h, ok := parseHandle(s["client_handle"])
			if !ok || !owners[h] {
				continue
			}
		}
		if active {
			if s["active"] == "1" {
				actives = append(actives, idx)
			}
			continue
		}
		letter, known := s["index_letter"]
		if !known && len(sel) == 1 {
			// Older firmware without index_letter: A=0, B=1, ...
			letter = string(rune('A' + idx))
		}
		if strings.EqualFold(letter, sel) {
			return idx, nil
		}
	}
	if active {
		switch len(actives) {
		case 0:
			return 0, errors.New("no active slice found")
		case 1:
			return actives[0], nil
		}
		// multiFLEX: every GUI client has its own active slice.
		return 0, fmt.Errorf("%d slices are active (multiFLEX); set the Station to choose one", len(actives))
	}
	return 0, fmt.Errorf("slice %s not found", strings.ToUpper(sel))
}
