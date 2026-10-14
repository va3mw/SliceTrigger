package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

// createDesktopShortcut puts SliceTrigger.lnk on the current user's desktop
// (wherever Windows has it, including a OneDrive-redirected desktop) and
// returns its path. It uses the WScript.Shell COM object via PowerShell; the
// paths go in through environment variables so no quoting is needed.
func createDesktopShortcut() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	script := `$ErrorActionPreference = 'Stop'
$d = [Environment]::GetFolderPath('Desktop')
$p = Join-Path $d 'SliceTrigger.lnk'
$s = (New-Object -ComObject WScript.Shell).CreateShortcut($p)
$s.TargetPath = $env:ST_TARGET
$s.WorkingDirectory = $env:ST_DIR
$s.IconLocation = $env:ST_TARGET + ',0'
$s.Description = 'SliceTrigger'
$s.Save()
Write-Output $p`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "ST_TARGET="+exe, "ST_DIR="+filepath.Dir(exe))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
