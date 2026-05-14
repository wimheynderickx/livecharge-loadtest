package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"
)

// fileBrowser wraps bubbles/filepicker so the user can navigate the
// filesystem and pick any .toml scenario via the 'b' shortcut.
//
// We keep our own thin wrapper around it because the dashboard needs to
// know when the bubble has actually selected a file (vs. just navigating);
// the bubble's Update signals this through the DidSelectFile helper.
type fileBrowser struct {
	open   bool
	fp     filepicker.Model
	ready  bool
	// status is shown under the picker. Set when an attempt to load the
	// selected file fails so the user sees the error without closing.
	status string
	// selected is the most recently picked absolute path. The dashboard
	// reads this after HandleKey reports "selected".
	selected string
}

// newFileBrowser constructs the bubble with our defaults: only .toml
// files are pickable, start directory is the first root (or cwd), hidden
// files are hidden.
func newFileBrowser(roots []string) fileBrowser {
	fp := filepicker.New()
	fp.AllowedTypes = []string{".toml"}
	fp.ShowHidden = false
	fp.AutoHeight = false
	fp.DirAllowed = false
	fp.FileAllowed = true

	start := "."
	if len(roots) > 0 && roots[0] != "" {
		start = roots[0]
	}
	if abs, err := absDir(start); err == nil {
		fp.CurrentDirectory = abs
	} else {
		fp.CurrentDirectory = start
	}

	return fileBrowser{fp: fp}
}

// Open shows the browser. Caller should also issue fp.Init() as a Cmd to
// trigger the first directory read.
func (b *fileBrowser) Open() tea.Cmd {
	b.open = true
	b.status = ""
	b.selected = ""
	return b.fp.Init()
}

// Close hides the browser without selecting anything.
func (b *fileBrowser) Close() { b.open = false }

// IsOpen reports visibility.
func (b *fileBrowser) IsOpen() bool { return b.open }

// SetSize updates the picker dimensions on terminal resize.
func (b *fileBrowser) SetSize(width, height int) {
	b.fp.Height = height - 4 // leave room for title/footer
	if b.fp.Height < 5 {
		b.fp.Height = 5
	}
	b.ready = true
}

// SetStatus sets a footer error message under the picker.
func (b *fileBrowser) SetStatus(s string) { b.status = s }

// Update forwards a message to the embedded picker. When the user picks a
// file, DidSelectFile reports it; we stash the path so the dashboard can
// retrieve it via Selected().
func (b *fileBrowser) Update(msg tea.Msg) (tea.Cmd, bool) {
	if !b.open {
		return nil, false
	}
	var cmd tea.Cmd
	b.fp, cmd = b.fp.Update(msg)

	if picked, path := b.fp.DidSelectFile(msg); picked {
		b.selected = path
		return cmd, true
	}
	if disabled, _ := b.fp.DidSelectDisabledFile(msg); disabled {
		b.status = "that file type is not allowed (only .toml)"
	}
	return cmd, false
}

// Selected returns the most recently picked path and clears the stash.
func (b *fileBrowser) Selected() string {
	p := b.selected
	b.selected = ""
	return p
}

// View renders the modal.
func (b *fileBrowser) View(width, height int) string {
	if !b.open {
		return ""
	}
	if !b.ready {
		return modalFrame(width, height, "Browse for scenario", "loading…", "[esc] close")
	}
	body := b.fp.View()
	if b.status != "" {
		body += "\n\n" + StyleErr.Render("  "+b.status)
	}
	return modalFrame(width, height,
		"Browse for scenario",
		body,
		"[↑↓] move  [→ / enter] open  [← / h] up  [enter on file] select  [esc] cancel",
	)
}

// absDir returns the absolute form of path. We use it to normalise the
// initial CurrentDirectory so the picker shows the expected root.
func absDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return path, err
	}
	if !info.IsDir() {
		return path, nil
	}
	if strings.HasPrefix(path, "/") {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return path, err
	}
	if path == "." {
		return wd, nil
	}
	return wd + "/" + path, nil
}
