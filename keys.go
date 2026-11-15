package main

import (
    "strings"
    "unicode/utf8"
)

// X11 keysyms used by the RFB KeyEvent message.
const (
    ksBackSpace = 0xff08
    ksTab       = 0xff09
    ksReturn    = 0xff0d
    ksEscape    = 0xff1b
    ksHome      = 0xff50
    ksLeft      = 0xff51
    ksUp        = 0xff52
    ksRight     = 0xff53
    ksDown      = 0xff54
    ksPageUp    = 0xff55
    ksPageDown  = 0xff56
    ksEnd       = 0xff57
    ksInsert    = 0xff63
    ksF1        = 0xffbe
    ksShiftL    = 0xffe1
    ksControlL  = 0xffe3
    ksAltL      = 0xffe9
    ksDelete    = 0xffff
)

// KeyEvent is one RFB key press or release.
type KeyEvent struct {
    Sym  uint32
    Down bool
}

// US layout characters typed with Shift.
const shifted = "~!@#$%^&*()_+{}|:\"<>?"

func press(sym uint32, mods ...uint32) []KeyEvent {
    var ev []KeyEvent
    for _, m := range mods {
        ev = append(ev, KeyEvent{m, true})
    }
    ev = append(ev, KeyEvent{sym, true}, KeyEvent{sym, false})
    for i := len(mods) - 1; i >= 0; i-- {
        ev = append(ev, KeyEvent{mods[i], false})
    }
    return ev
}

// tildeKeys maps "ESC [ n ~" sequences to keysyms.
var tildeKeys = map[string]uint32{
    "1": ksHome, "2": ksInsert, "3": ksDelete, "4": ksEnd,
    "5": ksPageUp, "6": ksPageDown, "7": ksHome, "8": ksEnd,
    "11": ksF1, "12": ksF1 + 1, "13": ksF1 + 2, "14": ksF1 + 3,
    "15": ksF1 + 4, "17": ksF1 + 5, "18": ksF1 + 6, "19": ksF1 + 7,
    "20": ksF1 + 8, "21": ksF1 + 9, "23": ksF1 + 10, "24": ksF1 + 11,
}

var finalKeys = map[byte]uint32{
    'A': ksUp, 'B': ksDown, 'C': ksRight, 'D': ksLeft,
    'H': ksHome, 'F': ksEnd,
    'P': ksF1, 'Q': ksF1 + 1, 'R': ksF1 + 2, 'S': ksF1 + 3,
}

// parseEscape decodes a terminal escape sequence at the start of b.
// It returns the events and the number of bytes consumed, or 0 if b
// does not start with a known sequence.
func parseEscape(b []byte) ([]KeyEvent, int) {
    if len(b) < 3 || b[0] != 0x1b || (b[1] != '[' && b[1] != 'O') {
        return nil, 0
    }
    if sym, ok := finalKeys[b[2]]; ok {
        return press(sym), 3
    }
    if b[1] == '[' {
        if end := strings.IndexByte(string(b[2:]), '~'); end > 0 {
            if sym, ok := tildeKeys[string(b[2:2+end])]; ok {
                return press(sym), 3 + end
            }
        }
    }
    return nil, 0
}

// KeysFromInput converts bytes read from a raw terminal into RFB key
// events, assuming a US keyboard layout on the guest side.
func KeysFromInput(b []byte) []KeyEvent {
    var ev []KeyEvent
    for i := 0; i < len(b); {
        c := b[i]
        switch {
        case c == 0x1b:
            if e, n := parseEscape(b[i:]); n > 0 {
                ev = append(ev, e...)
                i += n
                continue
            }
            if i+1 < len(b) && b[i+1] >= 0x20 && b[i+1] < 0x7f {
                // Alt+key
                sub := KeysFromInput(b[i+1 : i+2])
                ev = append(ev, KeyEvent{ksAltL, true})
                ev = append(ev, sub...)
                ev = append(ev, KeyEvent{ksAltL, false})
                i += 2
                continue
            }
            ev = append(ev, press(ksEscape)...)
        case c == '\r' || c == '\n':
            ev = append(ev, press(ksReturn)...)
        case c == '\t':
            ev = append(ev, press(ksTab)...)
        case c == 0x7f || c == 0x08:
            ev = append(ev, press(ksBackSpace)...)
        case c == 0:
            ev = append(ev, press(' ', ksControlL)...)
        case c < 0x1b:
            ev = append(ev, press(uint32('a'+c-1), ksControlL)...)
        case c < 0x20:
            ev = append(ev, press(uint32("\\]^_"[c-0x1c]), ksControlL)...)
        case c < 0x7f:
            if (c >= 'A' && c <= 'Z') || strings.IndexByte(shifted, c) >= 0 {
                ev = append(ev, press(uint32(c), ksShiftL)...)
            } else {
                ev = append(ev, press(uint32(c))...)
            }
        default:
            r, n := utf8.DecodeRune(b[i:])
            if r != utf8.RuneError {
                sym := uint32(r)
                if r > 0xff {
                    sym = 0x01000000 | uint32(r)
                }
                ev = append(ev, press(sym)...)
            }
            i += n
            continue
        }
        i++
    }
    return ev
}
