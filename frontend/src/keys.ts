// Turns a key press into the text a "keys" action holds ("CmdOrCtrl+Shift+4"), in the names
// internal/actions/combo.go reads.

const NAMED: Record<string, string> = {
  Space: "Space", Enter: "Enter", NumpadEnter: "Enter", Tab: "Tab", Escape: "Esc", Backspace: "Backspace", Delete: "Delete",
  ArrowUp: "Up", ArrowDown: "Down", ArrowLeft: "Left", ArrowRight: "Right",
  Home: "Home", End: "End", PageUp: "PageUp", PageDown: "PageDown",
  Comma: "Comma", Period: "Period", Minus: "Minus", Equal: "=", Slash: "/", Backslash: "\\", Semicolon: ";",
  Quote: "'", Backquote: "`", BracketLeft: "[", BracketRight: "]",
};

/** The key's name, or null for a modifier on its own or a key the action cannot press. */
export function keyName(code: string): string | null {
  if (NAMED[code]) return NAMED[code];
  if (/^Key[A-Z]$/.test(code)) return code.slice(3).toLowerCase();
  if (/^Digit\d$/.test(code)) return code.slice(5);
  if (/^F([1-9]|1\d|2[0-4])$/.test(code)) return code;
  return null;
}

export interface KeyLike {
  code: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

/**
 * The combination a key press makes. The main modifier is written CmdOrCtrl so a deck made on a
 * Mac works on Windows: Cmd on a Mac, Ctrl elsewhere. The other one stays what it is.
 */
export function comboOf(e: KeyLike, mac: boolean): string | null {
  const key = keyName(e.code);
  if (!key) return null;
  const mods: string[] = [];
  if (mac) {
    if (e.metaKey) mods.push("CmdOrCtrl");
    if (e.ctrlKey) mods.push("Ctrl");
  } else {
    if (e.ctrlKey) mods.push("CmdOrCtrl");
    if (e.metaKey) mods.push("Cmd");
  }
  if (e.altKey) mods.push("Alt");
  if (e.shiftKey) mods.push("Shift");
  return [...mods, key].join("+");
}
