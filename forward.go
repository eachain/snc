package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
)

type ForwardOptions struct {
	Server string `desc:"the server address forward to, format: 'host:port'"`
	Remote string `desc:"the remote host name or ip forward via"`
	Listen string `short:"l" long:"listen" desc:"local listen address, default is the port of server address"`
}

func TCPForward(ctx context.Context, opts *ForwardOptions) {
	host, port, _ := net.SplitHostPort(opts.Server)
	if host == "" || port == "" {
		fmt.Fprintln(os.Stderr, "server address invalid, format: 'host:port'")
		return
	}

	if opts.Remote == "" {
		fmt.Fprintln(os.Stderr, "remote host is empty")
		return
	}

	if opts.Listen == "" {
		opts.Listen = port
	}
	if !strings.Contains(opts.Listen, ":") {
		opts.Listen = "127.0.0.1:" + opts.Listen
	}

	client, err := NewSSHClient()
	if err != nil {
		return
	}

	listener, err := net.Listen("tcp4", opts.Listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "local listen %q: %v\n", opts.Listen, err)
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local accept conn: %v\n", err)
			continue
		}
		go forward(client, opts, host, port, conn)
	}
}

func forward(client *SSHClient, opts *ForwardOptions, host string, port string, conn net.Conn) {
	defer conn.Close()
	ssh, err := client.NewSession(opts.Remote)
	if err != nil {
		return
	}
	defer ssh.Close()
	defer ssh.Stdin.Close()
	// defer ssh.Quit()
	// defer ssh.WaitPS1()

	c1, h1, p1, e1 := AllocProxy()
	if e1 != nil {
		return
	}
	defer c1.Close()
	c2, h2, p2, e2 := AllocProxy()
	if e2 != nil {
		return
	}
	defer c2.Close()

	cmd := fmt.Sprintf("nc -4 -w %v --recv-only %v %v | nc -4 -w %v %v %v | nc -4 -w %v --send-only %v %v\r",
		Options.Wait, h1, p1, Options.Wait, host, port, Options.Wait, h2, p2)
	if Options.Debug {
		fmt.Println(cmd)
	}
	_, err = ssh.Stdin.Write([]byte(cmd))
	if err != nil {
		fmt.Fprintf(os.Stderr, "write cmd: %v\n", err)
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, err := io.Copy(NewRC4Writer(c1, p1), conn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local -> ssh: %v\n", err)
		}
		c1.Close()
	}()

	go func() {
		defer wg.Done()
		_, err := io.Copy(conn, NewRC4Reader(c2, p2))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ssh -> local: %v\n", err)
		}
		conn.Close()
	}()

	wg.Wait()
}
