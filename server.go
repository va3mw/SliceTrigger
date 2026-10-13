package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TriggerServer is the local HTTP listener N1MM (via "fire") talks to.
type TriggerServer struct {
	ln  net.Listener
	srv *http.Server
}

func StartTrigger(port int, b *Bridge) (*TriggerServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/fire/", func(w http.ResponseWriter, r *http.Request) {
		results := b.Fire(strings.TrimPrefix(r.URL.Path, "/fire/"))
		status := http.StatusOK
		if len(results) == 0 {
			status = http.StatusBadRequest
		}
		var sb strings.Builder
		for _, res := range results {
			if !res.OK {
				status = http.StatusInternalServerError
				if res.Name == "" {
					status = http.StatusNotFound
				}
			}
			sb.WriteString(res.String() + "\n")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		io.WriteString(w, sb.String())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "SliceTrigger")
		for _, a := range b.Config().Actions {
			fmt.Fprintf(w, "%d\t%s\t%s\n", a.ID, a.Name, typeLabels[a.Type])
		}
	})
	t := &TriggerServer{ln: ln, srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}}
	go t.srv.Serve(ln)
	return t, nil
}

func (t *TriggerServer) Port() int { return t.ln.Addr().(*net.TCPAddr).Port }

func (t *TriggerServer) Close() { t.srv.Close() }

// FireClient asks a running instance to fire spec.
func FireClient(port int, spec string) (string, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/fire/%s", port, url.PathEscape(spec)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return string(body), fmt.Errorf("%s", resp.Status)
	}
	return string(body), nil
}
