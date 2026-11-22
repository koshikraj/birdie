package mcpserver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// channelFlags are the Claude Code launch flags that enable channel servers.
var channelFlags = []string{"--dangerously-load-development-channels", "--channels"}

// ChannelLaunch reports whether the Claude Code session that started this
// process was launched with channels enabled for the MCP server named
// server. Claude Code's MCP handshake looks the same either way, so this
// reads the launch flags of the nearest Claude Code ancestor process. found
// is false when no such ancestor could be identified (another MCP client, or
// a platform where process info isn't available).
func ChannelLaunch(server string) (enabled, found bool) {
	pid := os.Getppid()
	for depth := 0; depth < 8 && pid > 1; depth++ {
		ppid, argv, err := procInfo(pid)
		if err != nil {
			return false, false
		}
		if isClaudeCode(argv) {
			return hasChannel(argv, server), true
		}
		pid = ppid
	}
	return false, false
}

func isClaudeCode(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch strings.TrimSuffix(filepath.Base(argv[0]), ".exe") {
	case "claude":
		return true
	case "node", "bun":
		// npm installs run as node .../@anthropic-ai/claude-code/cli.js
		return len(argv) > 1 && strings.Contains(argv[1], "claude-code")
	}
	return false
}

// hasChannel reports whether argv enables server:<server> through one of the
// channel flags, written as "--flag v1 v2", "--flag=v1,v2" or a mix.
func hasChannel(argv []string, server string) bool {
	want := "server:" + server
	for i := 0; i < len(argv); i++ {
		name, inline, hasInline := strings.Cut(argv[i], "=")
		if !isChannelFlag(name) {
			continue
		}
		var values []string
		if hasInline {
			values = append(values, inline)
		}
		for i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
			i++
			values = append(values, argv[i])
		}
		for _, v := range values {
			for _, item := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
				if item == want {
					return true
				}
			}
		}
	}
	return false
}

func isChannelFlag(name string) bool {
	for _, f := range channelFlags {
		if name == f {
			return true
		}
	}
	return false
}

// procInfo returns a process's parent PID and command line.
func procInfo(pid int) (ppid int, argv []string, err error) {
	if runtime.GOOS == "linux" {
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return 0, nil, err
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, nil, err
		}
		// The command name in field 2 may contain spaces; fields after it
		// start past the last ')'.
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
		if len(fields) < 2 {
			return 0, nil, fmt.Errorf("unexpected /proc/%d/stat", pid)
		}
		ppid, err = strconv.Atoi(fields[1])
		return ppid, strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00"), err
	}
	if runtime.GOOS == "windows" {
		return 0, nil, fmt.Errorf("process info not supported on %s", runtime.GOOS)
	}
	out, err := exec.Command("ps", "-o", "ppid=", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, nil, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, nil, fmt.Errorf("no process %d", pid)
	}
	ppid, err = strconv.Atoi(fields[0])
	return ppid, fields[1:], err
}

// procStart returns when a process started, in a form only compared for
// equality, or "" when it can't be told.
func procStart(pid int) string {
	if runtime.GOOS == "linux" {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return ""
		}
		// Field 22 is the start time; fields after the command name start at 3.
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
		if len(fields) < 20 {
			return ""
		}
		return fields[19]
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
