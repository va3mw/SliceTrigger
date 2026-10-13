package main

import (
	"context"
	"net"
	"regexp"
	"sort"
	"time"
)

type Radio struct {
	IP, Model, Nickname, Serial string
}

func (r Radio) String() string {
	s := r.IP
	if r.Model != "" {
		s += "  " + r.Model
	}
	if r.Nickname != "" {
		s += "  " + r.Nickname
	}
	return s
}

var kvRe = regexp.MustCompile(`([A-Za-z_]+)=([^\s\x00]*)`)

// parseDiscovery pulls key=value pairs out of a discovery datagram. The text
// payload follows a binary VITA-49 header, which the regexp simply skips.
func parseDiscovery(b []byte) (Radio, bool) {
	var r Radio
	for _, m := range kvRe.FindAllSubmatch(b, -1) {
		v := string(m[2])
		switch string(m[1]) {
		case "ip":
			r.IP = v
		case "model":
			r.Model = v
		case "nickname":
			r.Nickname = v
		case "serial":
			r.Serial = v
		}
	}
	return r, r.IP != "" || r.Model != ""
}

// Discover listens for radio discovery broadcasts on UDP 4992 for d.
func Discover(d time.Duration) ([]Radio, error) {
	lc := net.ListenConfig{Control: reuseControl}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":4992")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	pc.SetReadDeadline(time.Now().Add(d))

	found := map[string]Radio{}
	buf := make([]byte, 2048)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		r, ok := parseDiscovery(buf[:n])
		if !ok {
			continue
		}
		if r.IP == "" {
			if ua, ok := from.(*net.UDPAddr); ok {
				r.IP = ua.IP.String()
			}
		}
		found[r.IP] = r
	}
	out := make([]Radio, 0, len(found))
	for _, r := range found {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out, nil
}
