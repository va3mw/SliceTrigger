// SliceTrigger: lets N1MM Logger+ function keys trigger actions on devices
// N1MM cannot control via CAT.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	exe, _ := os.Executable()
	if spec, port, ok := fireRequest(exe, os.Args[1:]); ok {
		os.Exit(fireMain(spec, port))
	}
	runApp()
}

var fireNameRe = regexp.MustCompile(`(?i)(?:^|[-_ ])fire[-_ ]?([a-z0-9,]+)$`)

// fireRequest decides whether this process is a "fire" client and what to
// fire. It accepts:
//
//	SliceTrigger.exe fire 3 5        normal arguments
//	SliceTrigger.exe "fire 3,5"      everything in one quoted argument
//	SliceTrigger-fire-3.exe          a copy of the exe named after the action,
//	                                 for launchers that cannot pass arguments
func fireRequest(exePath string, args []string) (spec string, port int, ok bool) {
	var toks []string
	for _, a := range args {
		for _, t := range strings.Fields(a) {
			if t = strings.Trim(t, `"'`); t != "" {
				toks = append(toks, t)
			}
		}
	}
	if len(toks) > 0 && strings.EqualFold(toks[0], "fire") {
		toks = toks[1:]
		if len(toks) >= 2 && toks[0] == "-port" {
			port, _ = strconv.Atoi(toks[1])
			toks = toks[2:]
		}
		return strings.Join(toks, ","), port, true
	}
	name := filepath.Base(strings.ReplaceAll(exePath, `\`, "/"))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if m := fireNameRe.FindStringSubmatch(name); m != nil {
		return m[1], 0, true
	}
	return "", 0, false
}

// fireMain is the client mode: it asks the running instance to fire spec.
func fireMain(spec string, port int) int {
	if spec == "" {
		return 2
	}
	if port == 0 {
		cfg, _ := LoadConfig(configPath())
		port = cfg.Port
	}
	out, err := FireClient(port, spec)
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
