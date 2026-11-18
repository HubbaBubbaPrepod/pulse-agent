package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtime.GOOS == "windows" {
		if name == "powershell.exe" {
			name = filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			for i := range args {
				if i > 0 && args[i-1] == "-Command" {
					args[i] = `[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); ` + args[i]
				}
			}
		}
		if name == "nvidia-smi" {
			if _, err := exec.LookPath(name); err != nil {
				for _, candidate := range []string{filepath.Join(os.Getenv("SystemRoot"), "System32", "nvidia-smi.exe"), filepath.Join(os.Getenv("ProgramFiles"), "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe")} {
					if _, err := os.Stat(candidate); err == nil {
						name = candidate
						break
					}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &limitedBuffer{}
	err := cmd.Run()
	return out.Bytes(), err
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2*1024*1024 {
		return 0, fmt.Errorf("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func numeric(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }
func valueOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// Read the oldest page, stopping the child after a bounded batch instead of
// discarding the oldest entries when catching up after a network outage.
func journalPage(ctx context.Context, since, until time.Time) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", "--no-pager", "--output=json", "--since", since.Add(time.Microsecond).Format(time.RFC3339Nano), "--until", until.Format(time.RFC3339Nano))
	configureCommand(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &limitedBuffer{}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), 256*1024)
	var out limitedBuffer
	count := 0
	for scan.Scan() {
		if _, err := out.Write(append(bytes.Clone(scan.Bytes()), '\n')); err != nil {
			cancel()
			cmd.Wait()
			return nil, err
		}
		count++
		if count == 100 {
			cancel()
			break
		}
	}
	scanErr := scan.Err()
	waitErr := cmd.Wait()
	if count == 100 {
		return out.Bytes(), nil
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return out.Bytes(), waitErr
}
func systemLogs(ctx context.Context, since, until time.Time) ([]logEntry, string) {
	entries := []logEntry{}
	if runtime.GOOS == "linux" {
		raw, err := journalPage(ctx, since, until)
		if err != nil {
			return entries, "unavailable: journalctl or journal permissions"
		}
		for _, line := range strings.Split(string(raw), "\n") {
			var row struct {
				Message  string `json:"MESSAGE"`
				Unit     string `json:"_SYSTEMD_UNIT"`
				Priority string `json:"PRIORITY"`
				Stamp    string `json:"__REALTIME_TIMESTAMP"`
			}
			if json.Unmarshal([]byte(line), &row) != nil || row.Message == "" {
				continue
			}
			stamp, _ := strconv.ParseInt(row.Stamp, 10, 64)
			level := "INFO"
			p, _ := strconv.Atoi(row.Priority)
			if p <= 3 {
				level = "ERROR"
			} else if p == 4 {
				level = "WARN"
			}
			if time.UnixMicro(stamp).After(since) {
				entries = append(entries, logEntry{Time: time.UnixMicro(stamp).UTC().Format(time.RFC3339Nano), Level: level, Source: truncate(row.Unit, 128), Message: truncate(row.Message, 8192)})
			}
		}
		return entries, "available"
	}
	if runtime.GOOS == "windows" {
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $start=[DateTimeOffset]::FromUnixTimeMilliseconds(%d).LocalDateTime; $end=[DateTimeOffset]::FromUnixTimeMilliseconds(%d).LocalDateTime; $rows=@(Get-WinEvent -FilterHashtable @{LogName='System','Application';StartTime=$start;EndTime=$end} -Oldest -MaxEvents 100 -ErrorAction SilentlyContinue | Where-Object {$_.TimeCreated -gt $start} | ForEach-Object {[pscustomobject]@{time=$_.TimeCreated.ToUniversalTime().ToString('o');level=[int]$_.Level;source=$_.ProviderName;message=$_.Message}}); ConvertTo-Json -Compress -InputObject $rows`, since.UnixMilli(), until.UnixMilli())
		raw, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		if err != nil {
			return entries, "unavailable: Windows Event Log permissions"
		}
		var rows []struct {
			Time    string
			Level   int
			Source  string
			Message string
		}
		if json.Unmarshal(raw, &rows) != nil {
			return entries, "unavailable: Windows Event Log response"
		}
		for _, v := range rows {
			level := "INFO"
			if v.Level <= 2 {
				level = "ERROR"
			} else if v.Level == 3 {
				level = "WARN"
			}
			entries = append(entries, logEntry{Time: v.Time, Level: level, Source: truncate(v.Source, 128), Message: truncate(v.Message, 8192)})
		}
		return entries, "available"
	}
	return entries, "unavailable: unsupported OS"
}
