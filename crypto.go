package main

import (
	"crypto/rc4"
	"io"
	"math/rand/v2"
)

// ATTENTION: when send bytes to remote,
// you must avoid bytes seq "*2\r\n$4\r\n",
// which will cause an error: "read: connection reset by peer"

func newRC4(port string) *rc4.Cipher {
	var seed [32]byte
	copy(seed[:], port)
	cc8 := rand.NewChaCha8(seed)
	var key [256]byte
	cc8.Read(key[:])
	c, _ := rc4.NewCipher(key[:])
	return c
}

type rc4Reader struct {
	r io.Reader
	c *rc4.Cipher
}

func NewRC4Reader(r io.Reader, port string) io.Reader {
	return &rc4Reader{r: r, c: newRC4(port)}
}

func (rr *rc4Reader) Read(p []byte) (n int, err error) {
	n, err = rr.r.Read(p)
	if n > 0 {
		rr.c.XORKeyStream(p[:n], p[:n])
	}
	return
}

type rc4Writer struct {
	w io.Writer
	c *rc4.Cipher
}

func NewRC4Writer(w io.Writer, port string) io.Writer {
	return &rc4Writer{w: w, c: newRC4(port)}
}

func (rw *rc4Writer) Write(p []byte) (int, error) {
	if len(p) > 0 {
		rw.c.XORKeyStream(p, p)
	}
	return rw.w.Write(p)
}
