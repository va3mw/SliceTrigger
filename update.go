package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// updateBase is where "Check for updates" looks: the VERSION file and the exe
// on the main branch of the GitHub repository.
const updateBase = "https://raw.githubusercontent.com/va3mw/SliceTrigger/main/"

type UpdateInfo struct {
	Version string
	SHA256  string
}

// fetchLatest reads VERSION: line 1 the version, line 2 the exe's SHA-256.
func fetchLatest(base string) (UpdateInfo, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(base + "VERSION")
	if err != nil {
		return UpdateInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("VERSION: %s", resp.Status)
	}
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 4096))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 || len(lines[1]) != 64 {
		return UpdateInfo{}, errors.New("VERSION file is malformed")
	}
	return UpdateInfo{Version: lines[0], SHA256: strings.ToLower(lines[1])}, nil
}

// newerVersion reports whether version a is newer than b ("1.10.0" > "1.9.2").
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// downloadUpdate saves the new exe next to exePath as <exe>.new and checks
// its SHA-256 against the VERSION file.
func downloadUpdate(base string, info UpdateInfo, exePath string) (string, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(base + "SliceTrigger.exe")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	tmp := exePath + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("cannot write next to the exe: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 100<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != info.SHA256 {
		err = errors.New("downloaded file does not match the published checksum (GitHub may still be updating; try again in a few minutes)")
	}
	if err == nil && n < 1<<20 {
		err = errors.New("downloaded file is too small to be SliceTrigger.exe")
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// installUpdate swaps the downloaded exe into place. A running exe cannot be
// overwritten on Windows but can be renamed, so the old one becomes <exe>.old
// and is deleted at the next start.
func installUpdate(exePath, newPath string) error {
	old := exePath + ".old"
	os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, exePath); err != nil {
		os.Rename(old, exePath) // put the original back
		return err
	}
	return nil
}

// cleanupOldExe removes the previous exe left by an update.
func cleanupOldExe(exePath string) {
	os.Remove(exePath + ".old")
}
