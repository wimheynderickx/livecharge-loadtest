package tui

// confirmDialog is a tiny yes/no modal used by the 'x' (remove) shortcut.
// We keep it dumb: the dashboard sets prompt + accept callback when
// opening, then routes y/n/esc keys here.
type confirmDialog struct {
	open    bool
	prompt  string
	accept  func()
}

// Open shows the dialog with the given prompt and accept callback.
// Pressing 'y' runs accept; 'n' or 'esc' closes without running it.
func (c *confirmDialog) Open(prompt string, accept func()) {
	c.prompt = prompt
	c.accept = accept
	c.open = true
}

// Close hides the dialog without firing the callback.
func (c *confirmDialog) Close() { c.open = false }

// IsOpen reports visibility.
func (c *confirmDialog) IsOpen() bool { return c.open }

// HandleKey routes a key. Returns true when the dialog was closed
// (caller can drop further routing).
func (c *confirmDialog) HandleKey(key string) (closed bool, confirmed bool) {
	if !c.open {
		return false, false
	}
	switch key {
	case "y", "Y", "enter":
		c.open = false
		if c.accept != nil {
			c.accept()
		}
		return true, true
	case "n", "N", "esc", "q":
		c.open = false
		return true, false
	}
	return false, false
}

// View renders the dialog centred inside the content area.
func (c *confirmDialog) View(width, height int) string {
	if !c.open {
		return ""
	}
	return modalFrame(width, height, "Confirm", c.prompt, "[y] yes   [n/esc] cancel")
}
