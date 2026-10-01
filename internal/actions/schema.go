package actions

// Field is one parameter of an action, as the editor's inspector draws it. One schema gives the
// form and the checks that can be done before saving; the handlers still check what they run.
type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // string, text, number, bool, tribool (unset, true, false), enum, path, keys, list, action, actions
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Help     string   `json:"help,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
}

// Schema describes an action type.
type Schema struct {
	Type   string  `json:"type"`
	Label  string  `json:"label"`
	Group  string  `json:"group"`
	Help   string  `json:"help,omitempty"`
	Fields []Field `json:"fields"`
	// Runnable is false for the actions the deck handles itself (page, back): there is nothing for
	// the editor's Test button to run on the computer.
	Runnable bool `json:"runnable"`
}

func num(f float64) *float64 { return &f }

// Schemas describes every action type the editor offers, navigation included. A test keeps it in
// step with the registry.
func Schemas() []Schema {
	return []Schema{
		{Type: "page", Label: "Folder", Group: "Navigation", Help: "Opens another page of this deck.",
			Fields: []Field{{Name: "page", Label: "Page", Kind: "page", Required: true}}},
		{Type: "back", Label: "Back", Group: "Navigation", Help: "Goes back to the page before."},
		{Type: "open.url", Label: "Open site", Group: "Open", Runnable: true,
			Fields: []Field{{Name: "url", Label: "URL", Kind: "string", Required: true, Help: "With a scheme, like https://example.com"}}},
		{Type: "open.app", Label: "Open app", Group: "Open", Runnable: true,
			Fields: []Field{{Name: "app", Label: "App", Kind: "string", Required: true, Help: "An app name (Safari, firefox) or an exe"}}},
		{Type: "open.file", Label: "Open file", Group: "Open", Runnable: true,
			Fields: []Field{{Name: "path", Label: "Path", Kind: "path", Required: true}}},
		{Type: "keys", Label: "Hotkey", Group: "Keyboard", Runnable: true,
			Fields: []Field{{Name: "keys", Label: "Keys", Kind: "keys", Required: true, Help: "Press the combination to record it"}}},
		{Type: "type", Label: "Type text", Group: "Keyboard", Runnable: true,
			Fields: []Field{{Name: "text", Label: "Text", Kind: "text", Required: true}}},
		{Type: "media.play", Label: "Play / pause", Group: "Media", Runnable: true},
		{Type: "media.next", Label: "Next track", Group: "Media", Runnable: true},
		{Type: "media.prev", Label: "Previous track", Group: "Media", Runnable: true},
		{Type: "volume.up", Label: "Volume up", Group: "Audio", Runnable: true},
		{Type: "volume.down", Label: "Volume down", Group: "Audio", Runnable: true},
		{Type: "volume.set", Label: "Set volume", Group: "Audio", Runnable: true,
			Fields: []Field{{Name: "level", Label: "Level (%)", Kind: "number", Required: true, Min: num(0), Max: num(100)}}},
		{Type: "volume.mute", Label: "Mute sound", Group: "Audio", Runnable: true, Help: "Mutes or unmutes the speakers.",
			Fields: []Field{{Name: "mute", Label: "Mute", Kind: "tribool", Help: "Flip alternates. For a button that rings when muted, use a Toggle with Mute and Unmute."}}},
		{Type: "mic.mute", Label: "Mute microphone", Group: "Audio", Runnable: true, Help: "Mutes or unmutes the microphone.",
			Fields: []Field{{Name: "mute", Label: "Mute", Kind: "tribool", Help: "Flip alternates. For a button that rings when muted, use a Toggle with Mute and Unmute."}}},
		{Type: "lock", Label: "Lock screen", Group: "System", Runnable: true},
		{Type: "sleep", Label: "Sleep", Group: "System", Runnable: true},
		{Type: "screenshot", Label: "Screenshot", Group: "System", Runnable: true, Help: "Saves a picture of the screen to the Desktop (Pictures on Linux); on Windows it opens the Snipping Tool."},
		{Type: "run", Label: "Run command", Group: "Scripts", Runnable: true,
			Help: "Runs a program with arguments. Nothing in an argument is reinterpreted unless Shell is on.",
			Fields: []Field{
				{Name: "command", Label: "Command", Kind: "string", Required: true},
				{Name: "args", Label: "Arguments", Kind: "list", Help: "One per line"},
				{Name: "cwd", Label: "Working folder", Kind: "path"},
				{Name: "timeout", Label: "Timeout (s)", Kind: "number", Min: num(1), Max: num(600), Help: "30 if empty"},
				{Name: "shell", Label: "Run as a shell line", Kind: "bool", Help: "For pipes and the like. The command is then one line of shell."},
				{Name: "detach", Label: "Do not wait for it", Kind: "bool", Help: "For a program that stays open"},
			}},
		{Type: "delay", Label: "Delay", Group: "Composite", Runnable: true,
			Fields: []Field{{Name: "ms", Label: "Milliseconds", Kind: "number", Required: true, Min: num(0), Max: num(60000)}}},
		{Type: "multi", Label: "Multi", Group: "Composite", Runnable: true, Help: "Runs its steps in order and stops at the first that fails.",
			Fields: []Field{{Name: "steps", Label: "Steps", Kind: "actions", Required: true}}},
		{Type: "toggle", Label: "Toggle", Group: "Composite", Runnable: false, Help: "Alternates between two actions; it needs its button to keep its state.",
			Fields: []Field{{Name: "on", Label: "When turned on", Kind: "action", Required: true}, {Name: "off", Label: "When turned off", Kind: "action", Required: true}}},
	}
}
