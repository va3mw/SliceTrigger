package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TypeFlexRxAnt = "flex_rxant"
	TypeFlexRaw   = "flex_raw"
	TypeKMtronic  = "kmtronic"
	TypeHTTP      = "http_get"
	TypeTCP       = "tcp_send"
)

var actionTypes = []string{TypeFlexRxAnt, TypeFlexRaw, TypeKMtronic, TypeHTTP, TypeTCP}

var typeLabels = map[string]string{
	TypeFlexRxAnt: "Flex RX antenna",
	TypeFlexRaw:   "Flex raw command",
	TypeKMtronic:  "KMtronic relay",
	TypeHTTP:      "HTTP GET",
	TypeTCP:       "TCP send",
}

// KMtronic modes.
const (
	KMOn      = "on"      // FF<relay>01
	KMOff     = "off"     // FF<relay>00
	KMOnly    = "only"    // FFE0xx with only <relay> set
	KMPattern = "pattern" // FFE0xx with an explicit bit pattern
	KMAllOff  = "alloff"  // FFE000
)

var kmModes = []string{KMOn, KMOff, KMOnly, KMPattern, KMAllOff}

type Act struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`

	// Flex
	Slice   string `json:"slice,omitempty"`   // A..H or Active
	Antenna string `json:"antenna,omitempty"` // flex_rxant
	Cycle   string `json:"cycle,omitempty"`   // flex_rxant round robin: "ANT1,ANT2,RX_A"
	Command string `json:"command,omitempty"` // flex_raw, ';' separated

	// KMtronic
	Host    string `json:"host,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Relay   int    `json:"relay,omitempty"`
	Pattern string `json:"pattern,omitempty"` // hex byte, bit0 = relay 1

	// HTTP GET (User/Pass also used by KMtronic)
	URL  string `json:"url,omitempty"`
	User string `json:"user,omitempty"`
	Pass string `json:"pass,omitempty"`

	// TCP send
	Addr      string `json:"addr,omitempty"`
	Data      string `json:"data,omitempty"`
	ReadReply bool   `json:"read_reply,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

func (a Act) Summary() string {
	switch a.Type {
	case TypeFlexRxAnt:
		if list := splitList(a.Cycle); len(list) > 0 {
			return fmt.Sprintf("slice %s rxant round robin: %s", orDefault(a.Slice, "Active"), strings.Join(list, " > "))
		}
		return fmt.Sprintf("slice %s rxant=%s", orDefault(a.Slice, "Active"), a.Antenna)
	case TypeFlexRaw:
		return a.Command
	case TypeKMtronic:
		u, err := kmtronicURL(a)
		if err != nil {
			return err.Error()
		}
		return u
	case TypeHTTP:
		return a.URL
	case TypeTCP:
		return a.Addr + "  " + a.Data
	}
	return ""
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// splitList splits a comma separated list, dropping empty entries.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// nextAntenna picks the entry after cur in list, wrapping around. If cur is
// not in the list the first entry is used. Entries the radio does not offer
// for this slice (avail, from ant_list) are skipped when avail is known.
func nextAntenna(list []string, cur string, avail []string) string {
	if len(avail) > 0 {
		var usable []string
		for _, a := range list {
			for _, v := range avail {
				if strings.EqualFold(a, v) {
					usable = append(usable, a)
					break
				}
			}
		}
		if len(usable) > 0 {
			list = usable
		}
	}
	for i, a := range list {
		if strings.EqualFold(a, cur) {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}

// kmtronicURL builds the relay URL for a KMtronic WEB board.
func kmtronicURL(a Act) (string, error) {
	host := strings.TrimSpace(a.Host)
	if host == "" {
		return "", errors.New("KMtronic host is empty")
	}
	needRelay := func() error {
		if a.Relay < 1 || a.Relay > 8 {
			return fmt.Errorf("relay %d out of range 1..8", a.Relay)
		}
		return nil
	}
	var code string
	switch a.Mode {
	case KMOn, KMOff:
		if err := needRelay(); err != nil {
			return "", err
		}
		state := 0
		if a.Mode == KMOn {
			state = 1
		}
		code = fmt.Sprintf("FF%02X%02X", a.Relay, state)
	case KMOnly:
		if err := needRelay(); err != nil {
			return "", err
		}
		code = fmt.Sprintf("FFE0%02X", 1<<(a.Relay-1))
	case KMPattern:
		v, err := strconv.ParseUint(strings.TrimSpace(a.Pattern), 16, 8)
		if err != nil {
			return "", fmt.Errorf("pattern %q is not a hex byte", a.Pattern)
		}
		code = fmt.Sprintf("FFE0%02X", v)
	case KMAllOff:
		code = "FFE000"
	default:
		return "", fmt.Errorf("unknown KMtronic mode %q", a.Mode)
	}
	return "http://" + host + "/" + code, nil
}

// unescape expands \r \n \t \0 \\ and \xHH.
func unescape(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out = append(out, s[i])
			continue
		}
		i++
		if i >= len(s) {
			return nil, errors.New("trailing backslash")
		}
		switch s[i] {
		case 'r':
			out = append(out, '\r')
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case '0':
			out = append(out, 0)
		case '\\':
			out = append(out, '\\')
		case 'x':
			if i+2 >= len(s) {
				return nil, errors.New(`\x needs two hex digits`)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return nil, fmt.Errorf(`bad escape \x%s`, s[i+1:i+3])
			}
			out = append(out, byte(v))
			i += 2
		default:
			return nil, fmt.Errorf(`unknown escape \%c`, s[i])
		}
	}
	return out, nil
}

func tcpSend(a Act) (string, error) {
	data, err := unescape(a.Data)
	if err != nil {
		return "", err
	}
	timeout := time.Duration(a.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	conn, err := net.DialTimeout("tcp", a.Addr, timeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(data); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("sent %d bytes", len(data))
	if !a.ReadReply {
		return msg, nil
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if n == 0 {
		if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
			return msg + ", no reply", nil
		}
		return msg, err
	}
	return msg + ", reply " + strconv.Quote(string(buf[:n])), nil
}

type Result struct {
	Token string
	ID    int
	Name  string
	OK    bool
	Msg   string
}

func (r Result) String() string {
	st := "OK"
	if !r.OK {
		st = "ERR"
	}
	if r.Name == "" {
		return fmt.Sprintf("%s %s: %s", st, r.Token, r.Msg)
	}
	return fmt.Sprintf("%s %d %s: %s", st, r.ID, r.Name, r.Msg)
}

// Bridge owns the config and runs actions.
type Bridge struct {
	Flex *Flex
	Logf func(format string, args ...any)

	antMu sync.Mutex // serializes RX antenna changes so rapid presses step one at a time

	mu   sync.Mutex
	cfg  *Config
	path string
	http *http.Client
}

func NewBridge(cfg *Config, path string) *Bridge {
	return &Bridge{
		Flex: NewFlex(),
		Logf: func(string, ...any) {},
		cfg:  cfg,
		path: path,
		http: &http.Client{Timeout: 3 * time.Second},
	}
}

func (b *Bridge) Config() Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := *b.cfg
	c.Actions = append([]Act(nil), b.cfg.Actions...)
	return c
}

// Update mutates the config under lock and saves it.
func (b *Bridge) Update(fn func(c *Config)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(b.cfg)
	if b.path == "" {
		return nil
	}
	return b.cfg.Save(b.path)
}

func (b *Bridge) lookup(token string) (Act, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, err := strconv.Atoi(token); err == nil {
		for _, a := range b.cfg.Actions {
			if a.ID == id {
				return a, true
			}
		}
	}
	for _, a := range b.cfg.Actions {
		if strings.EqualFold(a.Name, token) {
			return a, true
		}
	}
	return Act{}, false
}

// Fire runs a comma separated list of action IDs or names, in order.
func (b *Bridge) Fire(spec string) []Result {
	var results []Result
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		res := Result{Token: tok}
		a, ok := b.lookup(tok)
		if !ok {
			res.Msg = "no such action"
		} else {
			res.ID, res.Name = a.ID, a.Name
			msg, err := b.Run(a)
			res.OK, res.Msg = err == nil, msg
			if err != nil {
				res.Msg = strings.TrimSpace(msg + " " + err.Error())
			}
		}
		b.Logf("%s", res)
		results = append(results, res)
	}
	return results
}

const flexTimeout = 3 * time.Second

func (b *Bridge) Run(a Act) (string, error) {
	b.mu.Lock()
	station := b.cfg.Station
	b.mu.Unlock()

	switch a.Type {
	case TypeFlexRxAnt:
		b.antMu.Lock()
		defer b.antMu.Unlock()
		idx, err := b.Flex.Resolve(a.Slice, station)
		if err != nil {
			return "", err
		}
		ant := strings.TrimSpace(a.Antenna)
		if list := splitList(a.Cycle); len(list) > 0 {
			ant = nextAntenna(list, b.Flex.SliceValue(idx, "rxant"), splitList(b.Flex.SliceValue(idx, "ant_list")))
		}
		if ant == "" {
			return "", errors.New("antenna is empty")
		}
		cmd := fmt.Sprintf("slice set %d rxant=%s", idx, ant)
		if _, err = b.Flex.Command(cmd, flexTimeout); err != nil {
			return cmd, err
		}
		b.Flex.setSliceValue(idx, "rxant", ant)
		return cmd, nil
	case TypeFlexRaw:
		var sent []string
		for _, cmd := range strings.Split(a.Command, ";") {
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				continue
			}
			if strings.Contains(cmd, "{slice}") {
				idx, err := b.Flex.Resolve(a.Slice, station)
				if err != nil {
					return strings.Join(sent, "; "), err
				}
				cmd = strings.ReplaceAll(cmd, "{slice}", strconv.Itoa(idx))
			}
			sent = append(sent, cmd)
			if _, err := b.Flex.Command(cmd, flexTimeout); err != nil {
				return strings.Join(sent, "; "), err
			}
		}
		if len(sent) == 0 {
			return "", errors.New("command is empty")
		}
		return strings.Join(sent, "; "), nil
	case TypeKMtronic:
		u, err := kmtronicURL(a)
		if err != nil {
			return "", err
		}
		return b.httpGet(u, a.User, a.Pass)
	case TypeHTTP:
		if strings.TrimSpace(a.URL) == "" {
			return "", errors.New("URL is empty")
		}
		return b.httpGet(strings.TrimSpace(a.URL), a.User, a.Pass)
	case TypeTCP:
		return tcpSend(a)
	}
	return "", fmt.Errorf("unknown action type %q", a.Type)
}

func (b *Bridge) httpGet(u, user, pass string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	msg := fmt.Sprintf("GET %s -> %s", u, resp.Status)
	if resp.StatusCode >= 400 {
		return msg, errors.New("request rejected")
	}
	return msg, nil
}
