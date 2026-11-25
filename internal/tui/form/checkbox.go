package form

// Checkbox is a labelled on/off choice; space toggles it.
type Checkbox struct {
	Label   string
	Checked bool
}

func (c *Checkbox) Toggle() { c.Checked = !c.Checked }

// View is "[x] Label" or "[ ] Label".
func (c Checkbox) View() string {
	box := "[ ] "
	if c.Checked {
		box = "[x] "
	}
	return box + c.Label
}
