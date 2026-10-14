package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// installDir is the recommended per-user location: %APPDATA%\SliceTrigger.
func installDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "SliceTrigger"), nil
}

func samePath(a, b string) bool {
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// installTo copies the running exe and its config into dir and returns the
// installed exe path. An exe already there is moved aside to .old (it may be
// running); an existing config there is kept as .bak and replaced by the
// current one, so the installed copy starts with the settings in use now.
func installTo(srcExe, srcConfig, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dstExe := filepath.Join(dir, "SliceTrigger.exe")
	if samePath(srcExe, dstExe) {
		return "", errors.New("SliceTrigger is already running from " + dir)
	}
	tmp := dstExe + ".new"
	if err := copyFile(srcExe, tmp); err != nil {
		return "", err
	}
	if _, err := os.Stat(dstExe); err == nil {
		if err := installUpdate(dstExe, tmp); err != nil {
			os.Remove(tmp)
			return "", err
		}
	} else if err := os.Rename(tmp, dstExe); err != nil {
		os.Remove(tmp)
		return "", err
	}

	dstConfig := filepath.Join(dir, configName)
	if _, err := os.Stat(srcConfig); err == nil && !samePath(srcConfig, dstConfig) {
		if _, err := os.Stat(dstConfig); err == nil {
			os.Remove(dstConfig + ".bak")
			if err := os.Rename(dstConfig, dstConfig+".bak"); err != nil {
				return dstExe, err
			}
		}
		if err := copyFile(srcConfig, dstConfig); err != nil {
			return dstExe, err
		}
	}
	return dstExe, nil
}
