package main

import (
    "bytes"
    "fmt"
    "net/url"
    "os"

    "golang.org/x/crypto/ssh/terminal"
    "github.com/gorilla/websocket"
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

    con, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        return err
    }

    done := make(chan error)
    state, _ := terminal.MakeRaw(0)
    term := terminal.NewTerminal(os.Stdin, "")
    defer terminal.Restore(0, state)

    // Print initial message
    msg := `
+-----------------------------------------+
|Connected. Type "Ctrl+[ d" to disconnect.|
+-----------------------------------------+

`
    term.Write([]byte(msg))

    // Read from nova console and send to client.
    go func() {
        for {
            _, message, err := con.ReadMessage()
            if err != nil {
                fmt.Println("Error: ", err)
                return
            }
            term.Write(message)
        }
    }()

    // Read from standard input and send to nova console.
    go func() {
        len := 4
        buf := bytes.NewBuffer(make([]byte, len))

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
                return
            } else {
                prevKey = b[0]
            }

            err = con.WriteMessage(websocket.BinaryMessage, b[0:nr])
            if err != nil {
                done <- err
                return

            }
        }
    }()

    return <-done
}
