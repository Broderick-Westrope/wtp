// Package launchd installs and removes the scheduled `wtp sync` job as a macOS
// launchd user agent. Everything the job needs (binary path, PATH, XDG
// overrides) is baked into the plist at install time, because launchd starts
// jobs with neither the user's shell environment nor a working directory.
package launchd

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// Label identifies the launchd job.
const Label = "dev.broderick-westrope.wtp.sync"

// DefaultTickMinutes is how often launchd wakes the job. The job runs
// `sync --scheduled`, which skips unless a sync is due, so this is only how
// often it checks: the sync cadence itself is the maintenance_interval
// setting, which can change without reinstalling the agent. A tick that finds
// nothing due stats one file and exits.
const DefaultTickMinutes = 15

const plistMode = 0o644

// ErrNotInstalled reports that no plist is present at the expected path.
var ErrNotInstalled = errors.New("launchd agent is not installed")

// PlistPath returns the agent's plist path in ~/Library/LaunchAgents.
func PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// Installed returns the plist bytes currently on disk, or an error wrapping
// ErrNotInstalled.
func Installed(plistPath string) ([]byte, error) {
	data, err := os.ReadFile(plistPath) //nolint:gosec // path is derived from the user's home directory
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return nil, fmt.Errorf("reading plist %s: %w", plistPath, err)
	}
	return data, nil
}

// ProgramPath extracts the executable path from a plist produced by Render:
// the first ProgramArguments entry. A package upgrade can move the binary out
// from under an installed agent, and this is how that is detected.
func ProgramPath(plist []byte) (string, error) {
	_, rest, ok := bytes.Cut(plist, []byte("<key>ProgramArguments</key>"))
	if !ok {
		return "", errors.New("plist has no ProgramArguments key")
	}
	_, rest, ok = bytes.Cut(rest, []byte("<string>"))
	if !ok {
		return "", errors.New("plist ProgramArguments has no entries")
	}
	path, _, ok := bytes.Cut(rest, []byte("</string>"))
	if !ok {
		return "", errors.New("plist ProgramArguments entry is unterminated")
	}
	return html.UnescapeString(string(bytes.TrimSpace(path))), nil
}

// Launchctl abstracts launchctl invocations so tests can stub them.
type Launchctl interface {
	// Bootout unloads the job. It errors when the job is not loaded; callers
	// ignore that for idempotency.
	Bootout(label string) error
	// Bootstrap loads the plist into the user's GUI domain.
	Bootstrap(plistPath string) error
}

// ExecLaunchctl runs the real launchctl binary against gui/<uid>.
type ExecLaunchctl struct{}

// Bootout unloads label from the user's GUI domain.
func (ExecLaunchctl) Bootout(label string) error {
	return runLaunchctl("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
}

// Bootstrap loads plistPath into the user's GUI domain.
func (ExecLaunchctl) Bootstrap(plistPath string) error {
	return runLaunchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
}

func runLaunchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Low priority and background process type keep a sync from competing with
// interactive work for CPU or disk.
var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{
	"xml": html.EscapeString,
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{xml .Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{xml .BinaryPath}}</string>
		<string>sync</string>
		<string>--scheduled</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
{{- range .Env}}
		<key>{{xml .Key}}</key>
		<string>{{xml .Value}}</string>
{{- end}}
	</dict>
	<key>StartInterval</key>
	<integer>{{.StartInterval}}</integer>
	<key>RunAtLoad</key>
	<false/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>Nice</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>{{xml .LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{xml .LogPath}}</string>
</dict>
</plist>
`))

// EnvVar is one environment variable baked into the plist.
type EnvVar struct {
	Key   string
	Value string
}

type plistData struct {
	Label         string
	BinaryPath    string
	Env           []EnvVar
	StartInterval int
	LogPath       string
}

// Render produces the plist for the sync agent. binaryPath and logPath must
// be absolute. env is written in the order given. tickMinutes <= 0 falls back
// to DefaultTickMinutes.
func Render(binaryPath, logPath string, env []EnvVar, tickMinutes int) ([]byte, error) {
	if tickMinutes <= 0 {
		tickMinutes = DefaultTickMinutes
	}
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:         Label,
		BinaryPath:    binaryPath,
		Env:           env,
		StartInterval: tickMinutes * 60, //nolint:mnd // minutes to seconds
		LogPath:       logPath,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering plist: %w", err)
	}
	return buf.Bytes(), nil
}

// Install writes the plist and (re)loads the job. The job is booted out
// first because a bare bootstrap errors when it is already loaded.
func Install(lc Launchctl, plistPath string, plist []byte) error {
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil { //nolint:gosec,mnd // standard LaunchAgents perms
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(plistPath, plist, plistMode); err != nil {
		return fmt.Errorf("writing plist %s: %w", plistPath, err)
	}
	_ = lc.Bootout(Label)
	if err := lc.Bootstrap(plistPath); err != nil {
		return fmt.Errorf("loading launchd agent: %w", err)
	}
	return nil
}

// Uninstall boots the job out and removes the plist; idempotent.
func Uninstall(lc Launchctl, plistPath string) error {
	_ = lc.Bootout(Label)
	if err := os.Remove(plistPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing plist %s: %w", plistPath, err)
	}
	return nil
}
