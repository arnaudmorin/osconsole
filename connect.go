package main

import (
    "bytes"
    "fmt"
    "net/url"
    "os"

    "golang.org/x/crypto/ssh/terminal"
    "golang.org/x/net/websocket"
)

type Connect struct {
    config Config
}

// Connect to an instance via nova console
// http://docs.openstack.org/developer/nova/testing/serial-console.html
//
// Type "Ctrl+[ q" to disconnect
func (c *Connect) Run() error {
    u, err := url.Parse(c.config.ConsoleUrl)
    if err != nil {
        return err
    }

    config, err := websocket.NewConfig(u.String(), u.Scheme+"://"+u.Host)
    if err != nil {
        return err
    }
    config.Protocol = []string{"binary", "base64"}
    config.Version = 13
    con, err := websocket.DialConfig(config)
    if err != nil {
        return err
    }

    done := make(chan error)
    state, _ := terminal.MakeRaw(0)
    term := terminal.NewTerminal(os.Stdin, "")
    defer terminal.Restore(0, state)

    // Print initial message
    msg := `
Connected. Type "Ctrl+[ d" to disconnect.
_________________________________________

`
    term.Write([]byte(msg))

    // Read from nova console and send to client.
    go func() {
        len := 32 * 1024
        buf := bytes.NewBuffer(make([]byte, len))
        for {
            buf.Truncate(len)
            b := buf.Bytes()

            n, err := con.Read(b)
            if err != nil {
                done <- err
                return

            } else if n == 0 {
                continue
            }
            term.Write(b[0:n])
        }
    }()

    // Read from standard input and send to nova console.
    go func() {
        len := 4
        buf := bytes.NewBuffer(make([]byte, len))
        fw, _ := con.NewFrameWriter(con.PayloadType)

        var prevKey byte
        for {
            buf.Truncate(len)
            b := buf.Bytes()

            nr, err := os.Stdin.Read(b)
            if err != nil {
                done <- err
                return

            } else if nr == 0 {
                continue
            }

            // Ctrl+[ d
            if prevKey == 0x1b && b[0] == 'd' {
                done <- nil
                term.Write([]byte(`Disconnecting...`))
                return
            } else {
                prevKey = b[0]
            }

            nw, err := fw.Write(b[0:nr])
            if err != nil {
                done <- err
                return

            } else if nr != nw {
                done <- fmt.Errorf("Short written")
            }
        }
    }()

    return <-done
}
