import { useEffect, useRef, useState } from "react";

// window.prompt and window.confirm cannot be used: the macOS webview (WKWebView under Wails)
// shows them only if the app implements their delegate calls, and Wails does not, so prompt
// returns null and confirm false without showing anything. These ask in the page instead, the
// same on every OS.

type Request = { id: number; text: string } & (
  | { kind: "ask"; value: string; done: (v: string | null) => void }
  | { kind: "confirm"; ok: string; done: (v: boolean) => void }
);

let show: ((r: Request | null) => void) | null = null;
let current: Request | null = null;
let ids = 0;

function open(r: Request) {
  // A question still open is answered as cancelled: only one shows at a time.
  if (current) finish(current, false);
  current = r;
  show?.(r);
}

function finish(r: Request, accepted: boolean, value = "") {
  if (current !== r) return;
  current = null;
  show?.(null);
  if (r.kind === "ask") r.done(accepted ? value.trim() : null);
  else r.done(accepted);
}

/** Asks for a line of text: the trimmed answer, or null when cancelled. */
export function ask(text: string, value = ""): Promise<string | null> {
  return new Promise((done) => open({ id: ++ids, kind: "ask", text, value, done }));
}

/** Asks yes or no; true when the user pressed the ok button. */
export function confirm(text: string, ok = "OK"): Promise<boolean> {
  return new Promise((done) => open({ id: ++ids, kind: "confirm", text, ok, done }));
}

/** Draws the open question. Mount it once, at the root. */
export function Dialogs() {
  const [req, setReq] = useState<Request | null>(current);
  useEffect(() => {
    show = setReq;
    return () => {
      show = null;
    };
  }, []);
  return req ? <Question key={req.id} req={req} /> : null;
}

function Question({ req }: { req: Request }) {
  const [value, setValue] = useState(req.kind === "ask" ? req.value : "");
  const input = useRef<HTMLInputElement>(null);
  const okBtn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (input.current) {
      input.current.focus();
      input.current.select();
    } else okBtn.current?.focus();
  }, []);
  const cancel = () => finish(req, false);
  const accept = () => finish(req, true, value);

  return (
    <div
      className="modal ask"
      onClick={cancel}
      onKeyDown={(e) => {
        // Kept from the editor's own shortcuts (Delete, ⌘Z) behind the dialog.
        e.stopPropagation();
        if (e.key === "Escape") cancel();
      }}
    >
      <form
        className="dialog small"
        onClick={(e) => e.stopPropagation()}
        onSubmit={(e) => {
          e.preventDefault();
          accept();
        }}
      >
        <p className="asktext">{req.text}</p>
        {req.kind === "ask" && <input ref={input} value={value} onChange={(e) => setValue(e.target.value)} spellCheck={false} />}
        <div className="row end">
          <button type="button" onClick={cancel}>
            Cancel
          </button>
          <button ref={okBtn} type="submit" className="primary">
            {req.kind === "confirm" ? req.ok : "OK"}
          </button>
        </div>
      </form>
    </div>
  );
}
