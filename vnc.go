package main

import (
    "bufio"
    "encoding/binary"
    "fmt"
    "io"
    "net/url"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "golang.org/x/crypto/ssh/terminal"
)

// VNCWebsocketURL converts the URL returned by
// "openstack console url show --novnc" (an HTML page with the token in
// the "path" query parameter) into the websocket URL of nova-novncproxy.
func VNCWebsocketURL(raw string) (string, error) {
    u, err := url.Parse(raw)
    if err != nil {
        return "", err
    }
    switch u.Scheme {
    case "http":
        u.Scheme = "ws"
    case "https":
        u.Scheme = "wss"
    case "ws", "wss":
        return u.String(), nil
    default:
        return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
    }
    q := u.Query()
    if p := q.Get("path"); p != "" {
        // noVNC opens <scheme>://<host>/<path>
        pu, err := url.Parse("/" + strings.TrimPrefix(p, "/"))
        if err != nil {
            return "", err
        }
        u.Path, u.RawPath, u.RawQuery = pu.Path, pu.RawPath, pu.RawQuery
    } else if tok := q.Get("token"); tok != "" {
        u.Path, u.RawQuery = "/", "token="+url.QueryEscape(tok)
    } else {
        return "", fmt.Errorf("no token found in %s", raw)
    }
    u.Fragment = ""
    return u.String(), nil
}

// wsStream exposes a websocket as a byte stream (RFB does not care about
// websocket message boundaries).
type wsStream struct {
    c   *websocket.Conn
    r   io.Reader
    wmu sync.Mutex
}

func (s *wsStream) Read(p []byte) (int, error) {
    for {
        if s.r == nil {
            _, r, err := s.c.NextReader()
            if err != nil {
                return 0, err
            }
            s.r = r
        }
        n, err := s.r.Read(p)
        if err == io.EOF {
            s.r = nil
            if n > 0 {
                return n, nil
            }
            continue
        }
        return n, err
    }
}

func (s *wsStream) Write(p []byte) (int, error) {
    s.wmu.Lock()
    defer s.wmu.Unlock()
    if err := s.c.WriteMessage(websocket.BinaryMessage, p); err != nil {
        return 0, err
    }
    return len(p), nil
}

// VNCClient is a minimal RFB 3.8 client: no authentication (nova's proxy
// handles the security with the compute node), 32 bits true color, Raw
// and CopyRect encodings.
type VNCClient struct {
    close func() error
    r     *bufio.Reader
    w    io.Writer
    Name string

    mu sync.Mutex // protects FB
    FB *Framebuffer

    // OnUpdate is called (with mu held) after each framebuffer update.
    OnUpdate func(fb *Framebuffer)
    OnBell   func()
}

const (
    encRaw         = 0
    encCopyRect    = 1
    encDesktopSize = -223
)

func DialVNC(wsURL string) (*VNCClient, error) {
    c, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        if resp != nil {
            return nil, fmt.Errorf("%v (%s)", err, resp.Status)
        }
        return nil, err
    }
    s := &wsStream{c: c}
    v := &VNCClient{close: c.Close, r: bufio.NewReaderSize(s, 1<<16),
        w: s}
    if err := v.handshake(); err != nil {
        c.Close()
        return nil, err
    }
    return v, nil
}

func (v *VNCClient) Close() error { return v.close() }

func (v *VNCClient) read(n int) ([]byte, error) {
    b := make([]byte, n)
    _, err := io.ReadFull(v.r, b)
    return b, err
}

func (v *VNCClient) u8() (uint8, error) {
    return v.r.ReadByte()
}

func (v *VNCClient) u16() (uint16, error) {
    b, err := v.read(2)
    if err != nil {
        return 0, err
    }
    return binary.BigEndian.Uint16(b), nil
}

func (v *VNCClient) u32() (uint32, error) {
    b, err := v.read(4)
    if err != nil {
        return 0, err
    }
    return binary.BigEndian.Uint32(b), nil
}

func (v *VNCClient) reason() string {
    n, err := v.u32()
    if err != nil || n > 4096 {
        return "unknown reason"
    }
    b, _ := v.read(int(n))
    return string(b)
}

func (v *VNCClient) handshake() error {
    ver, err := v.read(12)
    if err != nil {
        return fmt.Errorf("reading RFB version: %v", err)
    }
    if !strings.HasPrefix(string(ver), "RFB ") {
        return fmt.Errorf("not an RFB server (got %q)", ver)
    }
    if _, err := v.w.Write([]byte("RFB 003.008\n")); err != nil {
        return err
    }

    n, err := v.u8()
    if err != nil {
        return err
    }
    if n == 0 {
        return fmt.Errorf("server refused connection: %s", v.reason())
    }
    types, err := v.read(int(n))
    if err != nil {
        return err
    }
    hasNone := false
    for _, t := range types {
        hasNone = hasNone || t == 1
    }
    if !hasNone {
        return fmt.Errorf("server requires authentication (security "+
            "types %v), only None is supported", types)
    }
    if _, err := v.w.Write([]byte{1}); err != nil {
        return err
    }
    res, err := v.u32()
    if err != nil {
        return err
    }
    if res != 0 {
        return fmt.Errorf("security handshake failed: %s", v.reason())
    }

    // ClientInit: shared session, do not kick other viewers.
    if _, err := v.w.Write([]byte{1}); err != nil {
        return err
    }
    init, err := v.read(24)
    if err != nil {
        return err
    }
    w := int(binary.BigEndian.Uint16(init[0:]))
    h := int(binary.BigEndian.Uint16(init[2:]))
    nameLen := binary.BigEndian.Uint32(init[20:])
    if nameLen > 4096 {
        return fmt.Errorf("invalid desktop name length")
    }
    name, err := v.read(int(nameLen))
    if err != nil {
        return err
    }
    v.Name = string(name)
    v.FB = NewFramebuffer(w, h)

    // SetPixelFormat: 32 bpp, depth 24, little endian, true color,
    // red/green/blue max 255 with shifts 16/8/0 => 0x00RRGGBB.
    pf := []byte{0, 0, 0, 0,
        32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
    if _, err := v.w.Write(pf); err != nil {
        return err
    }

    encs := []int32{encCopyRect, encRaw, encDesktopSize}
    msg := []byte{2, 0, 0, byte(len(encs))}
    for _, e := range encs {
        msg = appendU32(msg, uint32(e))
    }
    _, err = v.w.Write(msg)
    return err
}

func (v *VNCClient) RequestUpdate(incremental bool) error {
    v.mu.Lock()
    w, h := v.FB.W, v.FB.H
    v.mu.Unlock()
    msg := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
    if incremental {
        msg[1] = 1
    }
    binary.BigEndian.PutUint16(msg[6:], uint16(w))
    binary.BigEndian.PutUint16(msg[8:], uint16(h))
    _, err := v.w.Write(msg)
    return err
}

func (v *VNCClient) SendKeys(events []KeyEvent) error {
    if len(events) == 0 {
        return nil
    }
    msg := make([]byte, 0, len(events)*8)
    for _, e := range events {
        down := byte(0)
        if e.Down {
            down = 1
        }
        msg = append(msg, 4, down, 0, 0)
        msg = appendU32(msg, e.Sym)
    }
    _, err := v.w.Write(msg)
    return err
}

// Run processes server messages until the connection ends.
func (v *VNCClient) Run() error {
    if err := v.RequestUpdate(false); err != nil {
        return err
    }
    for {
        t, err := v.u8()
        if err != nil {
            return err
        }
        switch t {
        case 0:
            if err := v.framebufferUpdate(); err != nil {
                return err
            }
            if err := v.RequestUpdate(true); err != nil {
                return err
            }
        case 1: // SetColourMapEntries (unused in true color)
            hdr, err := v.read(5)
            if err != nil {
                return err
            }
            if _, err := v.read(6 * int(binary.BigEndian.Uint16(
                hdr[3:]))); err != nil {
                return err
            }
        case 2:
            if v.OnBell != nil {
                v.OnBell()
            }
        case 3: // ServerCutText
            if _, err := v.read(3); err != nil {
                return err
            }
            n, err := v.u32()
            if err != nil {
                return err
            }
            if _, err := v.r.Discard(int(n)); err != nil {
                return err
            }
        default:
            return fmt.Errorf("unsupported server message type %d", t)
        }
    }
}

func (v *VNCClient) framebufferUpdate() error {
    hdr, err := v.read(3)
    if err != nil {
        return err
    }
    n := int(binary.BigEndian.Uint16(hdr[1:]))

    v.mu.Lock()
    defer v.mu.Unlock()
    for i := 0; i < n; i++ {
        rh, err := v.read(12)
        if err != nil {
            return err
        }
        x := int(binary.BigEndian.Uint16(rh[0:]))
        y := int(binary.BigEndian.Uint16(rh[2:]))
        w := int(binary.BigEndian.Uint16(rh[4:]))
        h := int(binary.BigEndian.Uint16(rh[6:]))
        enc := int32(binary.BigEndian.Uint32(rh[8:]))
        fb := v.FB

        switch enc {
        case encRaw:
            data, err := v.read(w * h * 4)
            if err != nil {
                return err
            }
            for row := 0; row < h; row++ {
                if y+row >= fb.H {
                    break
                }
                for col := 0; col < w && x+col < fb.W; col++ {
                    fb.Pix[(y+row)*fb.W+x+col] = binary.LittleEndian.
                        Uint32(data[(row*w+col)*4:]) & 0xffffff
                }
            }
        case encCopyRect:
            src, err := v.read(4)
            if err != nil {
                return err
            }
            sx := int(binary.BigEndian.Uint16(src[0:]))
            sy := int(binary.BigEndian.Uint16(src[2:]))
            if sx+w > fb.W || sy+h > fb.H || x+w > fb.W || y+h > fb.H {
                return fmt.Errorf("CopyRect out of bounds")
            }
            tmp := make([]uint32, w*h)
            for row := 0; row < h; row++ {
                copy(tmp[row*w:(row+1)*w],
                    fb.Pix[(sy+row)*fb.W+sx:(sy+row)*fb.W+sx+w])
            }
            for row := 0; row < h; row++ {
                copy(fb.Pix[(y+row)*fb.W+x:(y+row)*fb.W+x+w],
                    tmp[row*w:(row+1)*w])
            }
        case encDesktopSize:
            v.FB = NewFramebuffer(w, h)
        default:
            return fmt.Errorf("unsupported encoding %d", enc)
        }
    }
    if v.OnUpdate != nil {
        v.OnUpdate(v.FB)
    }
    return nil
}

// VNCOptions configures RunVNC.
type VNCOptions struct {
    URL        string
    Fonts      []string
    Color      bool
    Screenshot bool          // print the screen as text and exit
    Type       string        // keys to type before the screenshot
    Wait       time.Duration // settle time for screenshot mode
}

func RunVNC(o VNCOptions) error {
    fonts, err := LoadFonts(o.Fonts)
    if err != nil {
        return err
    }
    rec := NewRecognizer(fonts)

    wsURL, err := VNCWebsocketURL(o.URL)
    if err != nil {
        return err
    }
    v, err := DialVNC(wsURL)
    if err != nil {
        return err
    }
    defer v.Close()

    if o.Screenshot {
        return screenshot(v, rec, o)
    }
    return interactive(v, rec, o)
}

func screenshot(v *VNCClient, rec *Recognizer, o VNCOptions) error {
    updates := make(chan struct{}, 1)
    v.OnUpdate = func(*Framebuffer) {
        select {
        case updates <- struct{}{}:
        default:
        }
    }
    errc := make(chan error, 1)
    go func() { errc <- v.Run() }()

    // settle waits for the first update, then until the screen is idle.
    settle := func() error {
        select {
        case <-updates:
        case err := <-errc:
            return err
        case <-time.After(10 * time.Second):
            return fmt.Errorf("no framebuffer update received")
        }
        deadline := time.After(o.Wait)
        for {
            select {
            case <-updates:
            case err := <-errc:
                return err
            case <-deadline:
                return nil
            case <-time.After(500 * time.Millisecond):
                return nil
            }
        }
    }
    if err := settle(); err != nil {
        return err
    }
    if o.Type != "" {
        if err := v.SendKeys(KeysFromInput([]byte(o.Type))); err != nil {
            return err
        }
        time.Sleep(o.Wait)
    }

    v.mu.Lock()
    grid := rec.Recognize(v.FB)
    v.mu.Unlock()
    fmt.Print(GridText(grid))
    return nil
}

func interactive(v *VNCClient, rec *Recognizer, o VNCOptions) error {
    state, err := terminal.MakeRaw(0)
    if err != nil {
        return err
    }
    defer terminal.Restore(0, state)

    out := os.Stdout
    // Alternate screen; one status line on top, remote screen below.
    fmt.Fprint(out, "\x1b[?1049h\x1b[H\x1b[2J")
    defer fmt.Fprint(out, "\x1b[0m\x1b[?25h\x1b[?1049l")
    fmt.Fprintf(out, "\x1b[7m Connected to %s (VNC). Type \"Ctrl+[ d\" "+
        "to disconnect. \x1b[0m", v.Name)

    r := NewRenderer(out, o.Color, 2)
    v.OnUpdate = func(fb *Framebuffer) {
        r.Draw(rec.Recognize(fb))
    }
    v.OnBell = func() { out.Write([]byte{7}) }

    done := make(chan error, 2)
    go func() { done <- v.Run() }()
    go func() {
        buf := make([]byte, 256)
        var prev byte
        for {
            n, err := os.Stdin.Read(buf)
            if err != nil {
                done <- err
                return
            }
            b := buf[:n]
            // Ctrl+[ d, typed as two reads or in one go.
            if (prev == 0x1b && b[0] == 'd') ||
                (n >= 2 && b[n-2] == 0x1b && b[n-1] == 'd') {
                done <- nil
                return
            }
            prev = b[n-1]
            if err := v.SendKeys(KeysFromInput(b)); err != nil {
                done <- err
                return
            }
        }
    }()
    return <-done
}

// appendU32 appends v in network byte order.
func appendU32(b []byte, v uint32) []byte {
    return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
