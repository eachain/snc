package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type SSHClient struct {
	sc *ssh.Client
}

func NewSSHClient() (*SSHClient, error) {
	jumper, port, _ := net.SplitHostPort(Options.Jumper)
	if jumper == "" {
		jumper = Options.Jumper
	}
	if port == "" {
		port = "22"
	}
	if Options.Debug {
		fmt.Printf("%v ssh -p %v %v@%v\n", Dollar, port, Options.User, jumper)
	}
	client, err := newSSHClient()
	if err != nil {
		return nil, err
	}
	return &SSHClient{sc: client}, nil
}

func (client *SSHClient) Close() error {
	return client.sc.Close()
}

func (client *SSHClient) NewSession(host string) (*SSHSession, error) {
	ssh, err := newSession(client.sc)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			ssh.Close()
		}
	}()

	// wait input hostname
	_, err = DiscardUntil(ssh.Stdout, '>')
	if err != nil {
		fmt.Fprintf(os.Stderr, "discard until '>': %v\n", err)
		return nil, err
	}
	_, err = DiscardMany(ssh.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discard space before type host: %v\n", err)
		return nil, err
	}

	// input hostname
	fmt.Fprintf(ssh.Stdin, "%v\r", host)
	// whether connect to the host or any error
	err = ParseConnectHostError(ssh.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh connect to %q: %v\n", host, err)
		return nil, err
	}
	_, err = DiscardMany(ssh.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discard space after type host: %v\n", err)
		return nil, err
	}

	// disable ssh echo
	err = ssh.Run("stty -echo\r", false)
	if err != nil {
		return nil, err
	}

	ok = true
	return ssh, nil
}

func getEnvHome() string {
	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if ok && key == "HOME" {
			return value
		}
	}
	return ""
}

func loadPrivateKey() (ssh.Signer, error) {
	if Options.SSHKey == "" {
		home := getEnvHome()
		if home == "" {
			err := errors.New("ENV: `HOME` not found")
			fmt.Fprintln(os.Stderr, err)
			return nil, err
		}
		Options.SSHKey = filepath.Join(home, ".ssh/id_rsa")
	}
	key, err := os.ReadFile(Options.SSHKey)
	if err != nil {
		err = fmt.Errorf("unable to read private key %q: %w", Options.SSHKey, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		err = fmt.Errorf("unable to parse private key %q: %w", Options.SSHKey, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}
	return signer, nil
}

func newSSHClient() (*ssh.Client, error) {
	signer, err := loadPrivateKey()
	if err != nil {
		return nil, err
	}

	algorithms := ssh.SupportedAlgorithms()
	config := &ssh.ClientConfig{
		Config: ssh.Config{
			KeyExchanges: algorithms.KeyExchanges,
			Ciphers:      algorithms.Ciphers,
			MACs:         algorithms.MACs,
		},
		User: Options.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback:   ssh.InsecureIgnoreHostKey(),
		HostKeyAlgorithms: algorithms.HostKeys,
		Timeout:           time.Duration(Options.Wait) * time.Second,
	}

	client, err := ssh.Dial("tcp", Options.Jumper, config)
	if err != nil {
		err = fmt.Errorf("dial %q: %w", Options.Jumper, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}
	return client, nil
}

type SSHSession struct {
	client  *ssh.Client
	session *ssh.Session
	Stdin   io.WriteCloser
	Stdout  io.Reader
	Stderr  io.Reader
}

func (ss *SSHSession) Close() error {
	e1 := ss.session.Close()
	if ss.client != nil {
		e2 := ss.client.Close()
		if e1 == nil {
			return e2
		}
	}
	return e1
}

func newSession(client *ssh.Client) (ss *SSHSession, err error) {
	session, err := client.NewSession()
	if err != nil {
		err = fmt.Errorf("ssh open new session: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			session.Close()
		}
	}()

	stdin, err := session.StdinPipe()
	if err != nil {
		err = fmt.Errorf("ssh get stdin: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}
	defer func() {
		if !ok {
			stdin.Close()
		}
	}()
	stdout, err := session.StdoutPipe()
	if err != nil {
		err = fmt.Errorf("ssh get stdout: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		err = fmt.Errorf("ssh get stderr: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}
	// Request pseudo terminal
	if err = session.RequestPty("xterm", 40, 80, modes); err != nil {
		err = fmt.Errorf("ssh request for pseudo terminal: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	err = session.Shell()
	if err != nil {
		err = fmt.Errorf("ssh start shell: %w", err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	ok = true
	return &SSHSession{
		session: session,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	}, nil
}

func (ss *SSHSession) Run(cmd string, echo ...bool) error {
	ec := true
	if len(echo) > 0 {
		ec = echo[0]
	}
	if Options.Debug && ec {
		fmt.Println(cmd)
	}
	_, err := ss.Stdin.Write([]byte(cmd))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh run `%v` write cmd: %v\n", cmd, err)
		return err
	}
	return ss.WaitPS1()
}

func (ss *SSHSession) WaitPS1() error {
	_, err := DiscardUntil(ss.Stdout, '$')
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh wait PS1: %v\n", err)
		return err
	}
	_, err = DiscardMany(ss.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh wait PS1 done: %v\n", err)
		return err
	}
	return nil
}

func (ss *SSHSession) SendEOF() error {
	if Options.Debug {
		fmt.Println("^D")
	}
	_, err := ss.Stdin.Write([]byte{4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh send EOF: %v\n", err)
	}
	return err
}

func (ss *SSHSession) SendTERM() error {
	if Options.Debug {
		fmt.Println("^C")
	}
	_, err := ss.Stdin.Write([]byte{3})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh send TERM: %v\n", err)
	}
	return err
}

func (ss *SSHSession) Quit() error {
	err := ss.WaitPS1()
	if err != nil {
		return err
	}
	err = ss.SendEOF()
	if err != nil {
		return err
	}

	_, err = DiscardUntil(ss.Stdout, '>')
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh wait jumper: %v\n", err)
		return err
	}
	_, err = DiscardMany(ss.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh wait jumper done: %v\n", err)
		return err
	}

	return ss.SendEOF()
}
