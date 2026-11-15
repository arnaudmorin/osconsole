package main

import (
    "bytes"
    "fmt"
    "io"
    "strings"
)

// vgaToANSI maps a VGA color index (0-7) to the ANSI color number.
var vgaToANSI = [8]int{0, 4, 2, 6, 1, 5, 3, 7}

func sgr(fg, bg uint8) string {
    // Light gray and black are the console default foreground and
    // background: use the local terminal defaults instead so that the
    // output looks native whatever the terminal theme.
    if fg == 7 && bg == 0 {
        return "\x1b[0m"
    }
    f := 39
    if fg != 7 {
        f = 30 + vgaToANSI[fg&7]
        if fg >= 8 {
            f += 60
        }
    }
    b := 49
    if bg != 0 {
        b = 40 + vgaToANSI[bg&7]
        if bg >= 8 {
            b += 60
        }
    }
    return fmt.Sprintf("\x1b[0;%d;%dm", f, b)
}

// Renderer mirrors the recognized remote screen in the local terminal,
// only rewriting the rows that changed.
type Renderer struct {
    out     io.Writer
    color   bool
    top     int // terminal row (1-based) where the remote screen starts
    prev    [][]Cell
    curX    int
    curY    int
    hasCurs bool
}

func NewRenderer(out io.Writer, color bool, top int) *Renderer {
    return &Renderer{out: out, color: color, top: top}
}

func sameRow(a, b []Cell) bool {
    if len(a) != len(b) {
        return false
    }
    for i := range a {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

func (r *Renderer) Draw(grid [][]Cell) {
    var buf bytes.Buffer
    if len(grid) != len(r.prev) ||
        (len(grid) > 0 && len(grid[0]) != len(r.prev[0])) {
        // Resolution or font changed: start from a clean screen.
        fmt.Fprintf(&buf, "\x1b[0m\x1b[%d;1H\x1b[J", r.top)
        r.prev = nil
        r.hasCurs = false
    }

    for y, row := range grid {
        for x, c := range row {
            if c.Cursor {
                r.curX, r.curY, r.hasCurs = x, y, true
            }
        }
        if y < len(r.prev) && sameRow(row, r.prev[y]) {
            continue
        }
        fmt.Fprintf(&buf, "\x1b[%d;1H", r.top+y)
        last := ""
        for _, c := range row {
            if r.color {
                if s := sgr(c.FG, c.BG); s != last {
                    buf.WriteString(s)
                    last = s
                }
            }
            buf.WriteRune(c.R)
        }
        buf.WriteString("\x1b[0m\x1b[K")
    }

    // Keep the local cursor where the remote one is (or was last seen,
    // as the remote cursor blinks).
    if r.hasCurs {
        fmt.Fprintf(&buf, "\x1b[%d;%dH\x1b[?25h", r.top+r.curY, r.curX+1)
    } else {
        buf.WriteString("\x1b[?25l")
    }

    r.prev = make([][]Cell, len(grid))
    for y := range grid {
        r.prev[y] = append([]Cell(nil), grid[y]...)
    }
    r.out.Write(buf.Bytes())
}

// GridText returns the screen as plain text, trailing blanks removed.
func GridText(grid [][]Cell) string {
    lines := make([]string, len(grid))
    for y, row := range grid {
        var sb strings.Builder
        for _, c := range row {
            sb.WriteRune(c.R)
        }
        lines[y] = strings.TrimRight(sb.String(), " ")
    }
    for len(lines) > 0 && lines[len(lines)-1] == "" {
        lines = lines[:len(lines)-1]
    }
    return strings.Join(lines, "\n") + "\n"
}
