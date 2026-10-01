package helperkit

// Scale shrinks a BGRA picture to at most maxw wide, keeping the aspect, by averaging the source
// pixels each output pixel covers (a plain pick-one would shimmer on text). stride is the source row
// length in bytes. The alpha of the result is forced opaque: window grabs often leave it 0, which the
// Show would draw as transparent. A picture already narrow enough is only copied and made opaque.
func Scale(src []byte, sw, sh, stride, maxw int) (dst []byte, w, h int) {
	if sw <= 0 || sh <= 0 || stride < sw*4 || len(src) < (sh-1)*stride+sw*4 {
		return nil, 0, 0
	}
	w, h = sw, sh
	if maxw > 0 && sw > maxw {
		w = maxw
		h = sh * maxw / sw
		if h < 1 {
			h = 1
		}
	}
	dst = make([]byte, w*h*4)
	if w == sw && h == sh {
		for y := 0; y < h; y++ {
			copy(dst[y*w*4:(y+1)*w*4], src[y*stride:])
		}
	} else {
		for y := 0; y < h; y++ {
			y0, y1 := y*sh/h, (y+1)*sh/h
			if y1 <= y0 {
				y1 = y0 + 1
			}
			for x := 0; x < w; x++ {
				x0, x1 := x*sw/w, (x+1)*sw/w
				if x1 <= x0 {
					x1 = x0 + 1
				}
				var b, g, r, n uint32
				for yy := y0; yy < y1; yy++ {
					row := src[yy*stride:]
					for xx := x0; xx < x1; xx++ {
						p := row[xx*4:]
						b += uint32(p[0])
						g += uint32(p[1])
						r += uint32(p[2])
						n++
					}
				}
				o := (y*w + x) * 4
				dst[o], dst[o+1], dst[o+2] = byte(b/n), byte(g/n), byte(r/n)
			}
		}
	}
	for i := 3; i < len(dst); i += 4 {
		dst[i] = 255
	}
	return dst, w, h
}

// MapPoint maps a point in frame pixels (fw x fh) to a position inside a window of ww x wh pixels,
// clipped to the window: nothing outside it can be clicked.
func MapPoint(x, y, fw, fh, ww, wh int) (int, int) {
	if fw <= 0 || fh <= 0 {
		return 0, 0
	}
	x = min(max(x, 0), fw-1)
	y = min(max(y, 0), fh-1)
	return x * ww / fw, y * wh / fh
}
