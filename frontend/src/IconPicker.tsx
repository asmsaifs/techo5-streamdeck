import { useEffect, useMemo, useRef, useState } from "react";
import { errorText, lucideNames, lucideSVGs, uploadIcon, userIcons } from "./api";

const PAGE = 120;

/** A pop-up to choose a button's icon: a bundled line icon, a picture uploaded earlier, or a new one. */
export function IconPicker(p: { value?: string; onPick: (icon: string | undefined) => void; onClose: () => void }) {
  const [names, setNames] = useState<string[]>([]);
  const [mine, setMine] = useState<Record<string, string>>({});
  const [svgs, setSvgs] = useState<Record<string, string>>({});
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState(PAGE);
  const [err, setErr] = useState("");
  const file = useRef<HTMLInputElement>(null);

  useEffect(() => {
    lucideNames().then(setNames).catch((e) => setErr(errorText(e)));
    userIcons().then(setMine).catch((e) => setErr(errorText(e)));
  }, []);

  const found = useMemo(() => {
    const t = q.trim().toLowerCase();
    return t ? names.filter((n) => n.includes(t)) : names;
  }, [names, q]);
  const shown = found.slice(0, limit);

  // Draw only the icons on screen: asking for two thousand at once would be slow to send.
  useEffect(() => {
    const need = shown.filter((n) => !(n in svgs));
    if (need.length) lucideSVGs(need).then((m) => setSvgs((s) => ({ ...s, ...m }))).catch((e) => setErr(errorText(e)));
  }, [shown, svgs]);

  const upload = (f: File) => {
    const r = new FileReader();
    r.onload = () =>
      uploadIcon(f.name, String(r.result))
        .then((name) => p.onPick(name))
        .catch((e) => setErr(errorText(e)));
    r.readAsDataURL(f);
  };

  return (
    <div className="modal" onClick={p.onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <div className="row">
          <input autoFocus placeholder="Search icons" value={q} onChange={(e) => (setQ(e.target.value), setLimit(PAGE))} style={{ flex: 1 }} />
          <button onClick={() => file.current?.click()}>Upload picture…</button>
          <button onClick={() => p.onPick(undefined)}>No icon</button>
          <button onClick={p.onClose}>Close</button>
          <input ref={file} type="file" accept="image/png,image/jpeg" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        </div>
        {err && <p className="msg err">{err}</p>}
        {Object.keys(mine).length > 0 && (
          <>
            <h3>Your pictures</h3>
            <div className="icons">
              {Object.entries(mine).map(([n, url]) => (
                <button key={n} title={n} className={p.value === n ? "on" : ""} onClick={() => p.onPick(n)}>
                  <img src={url} width={28} height={28} alt={n} />
                </button>
              ))}
            </div>
          </>
        )}
        <h3>Icons ({found.length})</h3>
        <p className="hint">Brand logos are not in this set. Upload a picture for those.</p>
        <div className="icons">
          {shown.map((n) => (
            <button key={n} title={n.slice(7)} className={p.value === n ? "on" : ""} onClick={() => p.onPick(n)}>
              <span className="svg" dangerouslySetInnerHTML={{ __html: svgs[n] ?? "" }} />
            </button>
          ))}
        </div>
        {found.length > limit && <button onClick={() => setLimit(limit + PAGE)}>Show more</button>}
      </div>
    </div>
  );
}
