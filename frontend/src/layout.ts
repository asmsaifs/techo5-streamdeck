// The cell rectangles of a grid on a screen, the same arithmetic as internal/render/layout.go, so
// the editor's drop targets sit exactly on the cells in the preview picture.
import type { Grid } from "./model";

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export function cellRects(g: Grid, width: number, height: number): Rect[] {
  const scale = Math.min(width / 960, height / 480);
  const gap = Math.round(g.gap * scale);
  const cw = (width - gap * (g.cols + 1)) / g.cols;
  const ch = (height - gap * (g.rows + 1)) / g.rows;
  const out: Rect[] = [];
  for (let row = 0; row < g.rows; row++) {
    for (let col = 0; col < g.cols; col++) {
      const x = gap + col * (cw + gap);
      const y = gap + row * (ch + gap);
      const [x0, y0, x1, y1] = [x, y, x + cw, y + ch].map(Math.round);
      out.push({ x: x0, y: y0, w: x1 - x0, h: y1 - y0 });
    }
  }
  return out;
}
