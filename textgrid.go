package main

import (
    "math/bits"
    "sort"
    "time"
)

// Framebuffer holds the remote screen, one 0x00RRGGBB value per pixel.
type Framebuffer struct {
    W, H int
    Pix  []uint32
}

func NewFramebuffer(w, h int) *Framebuffer {
    return &Framebuffer{W: w, H: h, Pix: make([]uint32, w*h)}
}

// Cell is one recognized character of the text screen.
type Cell struct {
    R      rune
    FG, BG uint8 // VGA palette index (0-15)
    Cursor bool  // the cell carries the (underline) text cursor
}

// unknownRune is displayed for cells no glyph matches.
const unknownRune = '�'

type match struct {
    r      rune
    cursor bool
    ok     bool
}

// glyphSet groups the glyphs of all loaded fonts sharing one size.
type glyphSet struct {
    w, h   int
    exact  map[string]rune
    glyphs []Glyph
}

func runePriority(r rune) int {
    switch {
    case r == ' ':
        return 0
    case r > 0x20 && r < 0x7f:
        return 1
    case r >= 0xa0 && r <= 0xff:
        return 2
    case r >= 0x2500 && r <= 0x259f: // box drawing and blocks
        return 3
    default:
        return 4
    }
}

func rowsKey(rows []uint32) string {
    b := make([]byte, 0, len(rows)*4)
    for _, r := range rows {
        b = append(b, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
    }
    return string(b)
}

// layout is one way to cut the framebuffer into character cells.
type layout struct {
    gs       *glyphSet
    cw, ch   int // cell size in pixels (cw may be gs.w+1 for 9-dot VGA)
    cols     int
    rows     int
    cache    map[string]match
    fullMask uint32
}

func newLayout(gs *glyphSet, cw, fbW, fbH int) *layout {
    return &layout{
        gs: gs, cw: cw, ch: gs.h,
        cols: fbW / cw, rows: fbH / gs.h,
        cache:    make(map[string]match),
        fullMask: uint32(1)<<gs.w - 1,
    }
}

// Recognizer turns a framebuffer into a grid of characters by matching
// each character cell against the bitmaps of known console fonts.
type Recognizer struct {
    sets       []*glyphSet
    cur        *layout
    fbW, fbH   int
    lastDetect time.Time
}

func NewRecognizer(fonts []*Font) *Recognizer {
    bySize := map[[2]int]*glyphSet{}
    for _, f := range fonts {
        k := [2]int{f.Width, f.Height}
        gs := bySize[k]
        if gs == nil {
            gs = &glyphSet{w: f.Width, h: f.Height,
                exact: map[string]rune{}}
            bySize[k] = gs
        }
        for _, g := range f.Glyphs {
            key := rowsKey(g.Rows)
            if old, ok := gs.exact[key]; ok &&
                runePriority(old) <= runePriority(g.Rune) {
                continue
            }
            gs.exact[key] = g.Rune
        }
    }
    r := &Recognizer{}
    for _, gs := range bySize {
        // One glyph per distinct bitmap, most common characters first so
        // that they win ties in nearest-match search.
        for key, rn := range gs.exact {
            rows := make([]uint32, gs.h)
            for i := range rows {
                rows[i] = uint32(key[i*4]) | uint32(key[i*4+1])<<8 |
                    uint32(key[i*4+2])<<16 | uint32(key[i*4+3])<<24
            }
            gs.glyphs = append(gs.glyphs, Glyph{rn, rows})
        }
        sort.Slice(gs.glyphs, func(i, j int) bool {
            pi := runePriority(gs.glyphs[i].Rune)
            pj := runePriority(gs.glyphs[j].Rune)
            if pi != pj {
                return pi < pj
            }
            return gs.glyphs[i].Rune < gs.glyphs[j].Rune
        })
        r.sets = append(r.sets, gs)
    }
    sort.Slice(r.sets, func(i, j int) bool {
        if r.sets[i].h != r.sets[j].h {
            return r.sets[i].h < r.sets[j].h
        }
        return r.sets[i].w < r.sets[j].w
    })
    return r
}

// cellBits extracts the ink bitmap of a cell: the most frequent color is
// the background, everything else is ink.
func (l *layout) cellBits(fb *Framebuffer, col, row int,
    rows []uint32) (fg, bg uint32) {

    x0, y0 := col*l.cw, row*l.ch
    type cc struct {
        c uint32
        n int
    }
    var counts [8]cc
    nc := 0
    for y := 0; y < l.ch; y++ {
        line := fb.Pix[(y0+y)*fb.W+x0 : (y0+y)*fb.W+x0+l.cw]
        for _, p := range line {
            i := 0
            for ; i < nc && counts[i].c != p; i++ {
            }
            if i == nc {
                if nc == len(counts) {
                    continue // too many colors, not text
                }
                counts[i].c = p
                nc++
            }
            counts[i].n++
        }
    }
    sort.Slice(counts[:nc], func(i, j int) bool {
        return counts[i].n > counts[j].n
    })
    bg = counts[0].c
    fg = bg
    if nc > 1 {
        fg = counts[1].c
    }

    // Only the first gs.w columns are compared: the 9th column of VGA
    // text mode is either background or a copy of the 8th one.
    for y := 0; y < l.ch; y++ {
        line := fb.Pix[(y0+y)*fb.W+x0:]
        var r uint32
        for x := 0; x < l.gs.w; x++ {
            if line[x] != bg {
                r |= 1 << x
            }
        }
        rows[y] = r
    }
    return fg, bg
}

// nearest returns the closest glyph, ignoring the rows set in skip.
func (l *layout) nearest(rows []uint32, skip uint64) (rune, int) {
    best, bestD := rune(-1), 1<<30
    for _, g := range l.gs.glyphs {
        d := 0
        for y, r := range rows {
            if skip&(1<<y) != 0 {
                continue
            }
            d += bits.OnesCount32(r ^ g.Rows[y])
            if d >= bestD {
                break
            }
        }
        if d < bestD {
            best, bestD = g.Rune, d
            if d == 0 {
                break
            }
        }
    }
    return best, bestD
}

func (l *layout) match(rows []uint32) match {
    key := rowsKey(rows)
    if m, ok := l.cache[key]; ok {
        return m
    }
    m := l.matchSlow(rows)
    l.cache[key] = m
    return m
}

func (l *layout) matchSlow(rows []uint32) match {
    ink := 0
    for _, r := range rows {
        ink += bits.OnesCount32(r)
    }
    if ink == 0 {
        return match{r: ' ', ok: true}
    }
    if r, ok := l.gs.exact[rowsKey(rows)]; ok {
        return match{r: r, ok: true}
    }

    // Underline cursor: full rows in the lower half of the cell, drawn
    // over the character by QEMU (VGA text mode) or by fbcon.
    var skip uint64
    for y := l.ch / 2; y < l.ch; y++ {
        if rows[y] == l.fullMask {
            skip |= 1 << y
        }
    }
    if n := bits.OnesCount64(skip); n > 0 && n <= 4 {
        if r, d := l.nearest(rows, skip); d == 0 {
            return match{r: r, cursor: true, ok: true}
        }
    }

    // Small differences: anti-aliasing free fonts rarely differ by more
    // than a few pixels between distributions (e.g. VGA vs Fixed).
    tolerance := ink / 8
    if tolerance < 2 {
        tolerance = 2
    }
    if r, d := l.nearest(rows, 0); d <= tolerance {
        return match{r: r, ok: true}
    }

    // Block cursor or reverse video limited to one cell.
    inv := make([]uint32, len(rows))
    for i, r := range rows {
        inv[i] = r ^ l.fullMask
    }
    if r, d := l.nearest(inv, 0); d <= tolerance {
        return match{r: r, cursor: true, ok: true}
    }
    return match{r: unknownRune}
}

// score rates how well a layout explains the current screen.
func (l *layout) score(fb *Framebuffer) (good, bad int) {
    rows := make([]uint32, l.ch)
    for row := 0; row < l.rows; row++ {
        for col := 0; col < l.cols; col++ {
            l.cellBits(fb, col, row, rows)
            m := l.match(rows)
            switch {
            case !m.ok:
                bad++
            case m.r != ' ':
                good++
            }
        }
    }
    return good, bad
}

// detect picks the cell size that best explains the framebuffer.
func (r *Recognizer) detect(fb *Framebuffer) {
    r.fbW, r.fbH = fb.W, fb.H
    r.lastDetect = time.Now()
    var best *layout
    bestScore, bestWaste := -1<<30, 1<<30
    for _, gs := range r.sets {
        for _, cw := range []int{gs.w, gs.w + 1} {
            if cw > fb.W || gs.h > fb.H {
                continue
            }
            l := newLayout(gs, cw, fb.W, fb.H)
            good, bad := l.score(fb)
            s := good - 2*bad
            // Prefer the layout leaving the smallest unused border when
            // the screen gives no hint (e.g. blank screen).
            waste := fb.W%cw*fb.H + fb.H%gs.h*fb.W
            if s > bestScore || (s == bestScore && waste < bestWaste) {
                best, bestScore, bestWaste = l, s, waste
            }
        }
    }
    r.cur = best
}

// Recognize converts the framebuffer into rows of cells.
func (r *Recognizer) Recognize(fb *Framebuffer) [][]Cell {
    if r.cur == nil || fb.W != r.fbW || fb.H != r.fbH {
        r.detect(fb)
    }
    if r.cur == nil {
        return nil
    }
    grid, good, bad := r.recognize(fb)
    // Guest changed font or mode: try again, at most once per second.
    if bad > 5 && bad*5 > good && time.Since(r.lastDetect) > time.Second {
        r.detect(fb)
        grid, _, _ = r.recognize(fb)
    }
    return grid
}

func (r *Recognizer) recognize(fb *Framebuffer) ([][]Cell, int, int) {
    l := r.cur
    grid := make([][]Cell, l.rows)
    rows := make([]uint32, l.ch)
    good, bad := 0, 0
    for row := 0; row < l.rows; row++ {
        grid[row] = make([]Cell, l.cols)
        for col := 0; col < l.cols; col++ {
            fg, bg := l.cellBits(fb, col, row, rows)
            m := l.match(rows)
            if !m.ok {
                bad++
            } else if m.r != ' ' {
                good++
            }
            c := Cell{R: m.r, FG: vgaIndex(fg), BG: vgaIndex(bg),
                Cursor: m.cursor}
            if c.R == ' ' && !c.Cursor {
                // No ink: the foreground color is meaningless.
                c.FG = 7
            }
            grid[row][col] = c
        }
    }
    return grid, good, bad
}

// vgaPalette is the standard 16 color VGA text mode palette.
var vgaPalette = [16]uint32{
    0x000000, 0x0000aa, 0x00aa00, 0x00aaaa,
    0xaa0000, 0xaa00aa, 0xaa5500, 0xaaaaaa,
    0x555555, 0x5555ff, 0x55ff55, 0x55ffff,
    0xff5555, 0xff55ff, 0xffff55, 0xffffff,
}

func vgaIndex(c uint32) uint8 {
    best, bestD := 0, 1<<30
    for i, p := range vgaPalette {
        dr := int(c>>16&0xff) - int(p>>16&0xff)
        dg := int(c>>8&0xff) - int(p>>8&0xff)
        db := int(c&0xff) - int(p&0xff)
        if d := dr*dr + dg*dg + db*db; d < bestD {
            best, bestD = i, d
        }
    }
    return uint8(best)
}
