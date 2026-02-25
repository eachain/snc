package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"time"
)

func DiscardUntil(r io.Reader, bs ...byte) ([]byte, error) {
	buf := make([]byte, 0, 32*1024)
	if Options.Debug {
		defer func() {
			if len(buf) > 0 {
				os.Stdout.Write(buf)
			}
		}()
	}

	var p [1]byte
	for {
		n, err := r.Read(p[:])
		if n == 1 {
			buf = append(buf, p[0])
			if slices.Contains(bs, p[0]) {
				return buf, err
			}
		}
		if err != nil {
			return buf, err
		}
	}
}

func DiscardMany(r io.Reader, n ...int) (int, error) {
	size := -1
	if len(n) > 0 && n[0] >= 0 {
		size = n[0]
	}
	if size == 0 {
		return 0, nil
	}
	if size < 0 {
		size = 1024 * 1024
	}
	discard := make([]byte, size)
	discarded, err := r.Read(discard)
	if discarded > 0 {
		if Options.Debug {
			os.Stdout.Write(discard[:discarded])
		}
	}
	return discarded, err
}

func ParseConnectHostError(r io.Reader) error {
	output, err := DiscardUntil(r, '$', '>')
	if err != nil {
		return err
	}
	if bytes.HasSuffix(output, []byte{'$'}) {
		return nil
	}
	lines := bytes.Split(output, []byte{'\n'})
	return errors.New(string(bytes.Join(lines[:len(lines)-1], []byte{'\n'})))
}

func AllocProxy() (conn net.Conn, host, port string, err error) {
	host, port, err = net.SplitHostPort(Options.Proxy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "split proxy host port %q: %v\n", Options.Proxy, err)
		return
	}

	conn, err = net.DialTimeout("tcp4", Options.Proxy, time.Duration(Options.Wait)*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	debug := Options.Debug
	Options.Debug = false
	line, err := DiscardUntil(conn, '\n')
	Options.Debug = debug
	if err != nil {
		fmt.Fprintf(os.Stderr, "read allocated port: %v\n", err)
		return
	}
	if len(line) < 2 {
		err = errors.New("empty port")
		fmt.Fprintf(os.Stderr, "read allocated port: %v\n", err)
		return
	}
	port = string(line[:len(line)-1])
	conn.SetReadDeadline(time.Time{})
	return
}
