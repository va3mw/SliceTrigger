// SliceTrigger: lets N1MM Logger+ function keys trigger actions on devices
// N1MM cannot control via CAT.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fire" {
		os.Exit(fireMain(os.Args[2:]))
	}
	runApp()
}

// fireMain is the client mode: "SliceTrigger.exe fire [-port N] 3 [5 ...]".
func fireMain(args []string) int {
	port := 0
	if len(args) >= 2 && args[0] == "-port" {
		port, _ = strconv.Atoi(args[1])
		args = args[2:]
	}
	if len(args) == 0 {
		return 2
	}
	if port == 0 {
		cfg, _ := LoadConfig(configPath())
		port = cfg.Port
	}
	out, err := FireClient(port, strings.Join(args, ","))
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
