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
		{Type: "stream.web", Label: "Show website", Group: "Stream",
			Help: "Shows a website on the Show and passes its touches to it. A tap does what a click does and a drag scrolls. The page's sound plays on the Show if it can (see Sound). Swipe in from the left edge to leave.",
			Fields: []Field{
				{Name: "url", Label: "URL", Kind: "string", Required: true, Help: "With a scheme, like https://example.com"},
				{Name: "allow", Label: "Other sites it may use", Kind: "list", Help: "One per line, like accounts.google.com. The page may only be at the site above and these; a link anywhere else does nothing."},
				{Name: "video", Label: "Video", Kind: "bool", Help: "For a page that is mostly a video: always sent at half size and 25 fps, so it moves smoothly. Without it the deck still does this by itself when the whole page has been moving for two seconds."},
				{Name: "sound", Label: "Sound", Kind: "enum", Enum: []string{"show", "desktop", "off"}, Help: "Where the page's sound goes. show (the default): the Show, and not this computer. desktop: this computer, as usual. off: nowhere. A Show with an older TECHO5 that cannot play sound gets none."},
				{Name: "av_offset_ms", Label: "Sound delay (ms)", Kind: "number", Min: num(0), Max: num(1000), Help: "Delays the sound so that it meets a video that arrives late. Try 100 to 200 if the sound is ahead of the picture."},
				{Name: "profile", Label: "Browser profile", Kind: "string", Help: "Which sign-ins it has. Empty: one per site, shared by every button for it. Lower case letters, digits, - and _."},
			}},
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
		{Type: "http", Label: "Web request", Group: "Web", Runnable: true,
			Help: "Sends one request, like a webhook. It worked if the answer is not an error.",
			Fields: []Field{
				{Name: "method", Label: "Method", Kind: "enum", Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, Help: "GET if empty"},
				{Name: "url", Label: "URL", Kind: "string", Required: true, Help: "http:// or https://"},
				{Name: "headers", Label: "Headers", Kind: "list", Help: "One per line, Name: value"},
				{Name: "body", Label: "Body", Kind: "text"},
				{Name: "timeout", Label: "Timeout (s)", Kind: "number", Min: num(1), Max: num(120), Help: "10 if empty"},
			}},
		{Type: "ha.service", Label: "Home Assistant service", Group: "Web", Runnable: true,
			Help: "Calls a service. The address is in Settings and the token is kept in the system keychain.",
			Fields: []Field{
				{Name: "service", Label: "Service", Kind: "string", Required: true, Help: "Like light.toggle"},
				{Name: "entity", Label: "Entity", Kind: "string", Help: "Like light.kitchen"},
				{Name: "data", Label: "More data", Kind: "text", Help: "A JSON object, like {\"brightness\": 120}"},
			}},
		{Type: "obs", Label: "OBS", Group: "Web", Runnable: true,
			Help: "Controls OBS Studio over its WebSocket server (Tools > WebSocket Server Settings). The address is in Settings.",
			Fields: []Field{
				{Name: "command", Label: "Command", Kind: "enum", Required: true, Enum: []string{"scene", "record.toggle", "record.start", "record.stop", "stream.toggle", "stream.start", "stream.stop", "input.mute.toggle"}},
				{Name: "name", Label: "Scene or input", Kind: "string", Help: "For scene and input.mute.toggle"},
			}},
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
