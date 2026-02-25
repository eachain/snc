//go:build ignore

// go build -ldflags='-w -s' sncd.go crypto.go

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

func NowString() string {
	return time.Now().Format("2006-01-02 15:04:05.000")
}

func handle(c1 *net.TCPConn, timeout time.Duration) {
	defer c1.Close()

	listener, err := net.Listen("tcp4", ":0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v] try listen rand tcp4 port: %v\n",
			NowString(), c1.RemoteAddr(), err)
		return
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v] split rand tcp4 addr %q: %v\n",
			NowString(), c1.RemoteAddr(), listener.Addr().String(), err)
		return
	}
	// fmt.Fprintf(os.Stderr, "%v [%v<->%v] start listen port\n",
	// 	NowString(), c1.RemoteAddr(), port)

	err = c1.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v<->%v] set write deadline: %v\n",
			NowString(), c1.RemoteAddr(), port, err)
		return
	}
	_, err = fmt.Fprintln(c1, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v<->%v] write tcp4 port: %v\n",
			NowString(), c1.RemoteAddr(), port, err)
		return
	}
	err = c1.SetWriteDeadline(time.Time{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v<->%v] unset write deadline: %v\n",
			NowString(), c1.RemoteAddr(), port, err)
		return
	}

	quit := make(chan struct{})
	once := new(sync.Once)
	stop := func() { once.Do(func() { close(quit) }) }
	defer stop()
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			listener.Close()
		case <-quit:
		}
	}()

	conn, err := listener.Accept()
	listener.Close()
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v [%v<->%v] accept: %v\n",
			NowString(), c1.RemoteAddr(), port, err)
		return
	}
	defer conn.Close()
	c2 := conn.(*net.TCPConn)

	fmt.Fprintf(os.Stderr, "%v [%v<->%v] pipe with [%v]\n",
		NowString(), c1.RemoteAddr(), port, c2.RemoteAddr())
	start := time.Now()
	var up, down int64
	defer func() {
		fmt.Fprintf(os.Stderr, "%v [%v<->%v] pipe with [%v]: up %v bytes, down %v bytes, elapsed %v\n",
			NowString(), c1.RemoteAddr(), port, c2.RemoteAddr(), up, down, time.Since(start))
	}()

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		defer wg.Done()
		var err error
		down, err = io.Copy(NewRC4Writer(c1, port), c2)
		c2.CloseRead()
		c1.CloseWrite()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintf(os.Stderr, "%v [%v<->%v] read from [%v]: %v\n",
				NowString(), c1.RemoteAddr(), port, c2.RemoteAddr(), err)
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		up, err = io.Copy(c2, NewRC4Reader(c1, port))
		c1.CloseRead()
		c2.CloseWrite()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintf(os.Stderr, "%v [%v<->%v] write to [%v]: %v\n",
				NowString(), c1.RemoteAddr(), port, c2.RemoteAddr(), err)
		}
	}()

	wg.Wait()
}

func main() {
	var port string
	var timeout int64
	flag.StringVar(&port, "p", "65533", "listen port without host and ':'")
	flag.Int64Var(&timeout, "t", 60, "random port listen timeout, unit: second")
	flag.Parse()

	listener, err := net.Listen("tcp4", ":"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen tcp4 port %v: %v\n", port, err)
		os.Exit(1)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v tcp4 port %v accept: %v\n", NowString(), port, err)
			continue
		}
		go handle(conn.(*net.TCPConn), time.Duration(timeout)*time.Second)
	}
}
