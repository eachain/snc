package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/crypto/ssh"
)

type RsyncOptions struct {
	Listen string `short:"l" long:"listen" dft:"65222" desc:"local ssh server listen address"`
	Update bool   `short:"u" long:"update" desc:"skip files that are newer on the receiver"`
	Remote string `required:"true" desc:"the remote or target, if remote, then download, else upload"`
	Target string `desc:"the remote or target"`

	upload bool
}

func Rsync(ctx context.Context, opts *RsyncOptions) {
	if strings.Contains(opts.Remote, ":") { // download
	} else if strings.Contains(opts.Target, ":") { // upload
		opts.upload = true
		opts.Remote, opts.Target = opts.Target, opts.Remote
		if _, err := os.Stat(opts.Target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	} else if _, err := os.Stat(opts.Remote); err == nil { // upload
		opts.upload = true
		opts.Remote, opts.Target = opts.Target, opts.Remote
		if opts.Remote == "" {
			fmt.Fprintln(os.Stderr, "no remote specified")
			return
		}
	} else { // download
		fmt.Fprintln(os.Stderr, "no file specified")
		return
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, pubKey ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(pubKey),
				},
			}, nil
		},
	}
	private, err := loadPrivateKey()
	if err != nil {
		return
	}
	config.AddHostKey(private)

	client, err := NewSSHClient()
	if err != nil {
		return
	}
	defer client.Close()

	listener, err := net.Listen("tcp4", "127.0.0.1:"+opts.Listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "local ssh server listen on port %q: %v\n", opts.Listen, err)
		return
	}
	defer listener.Close()

	wait, cancel, err := StartRsync(ctx, listener.Addr().String(), opts)
	if err != nil {
		return
	}
	defer wait()

	rsync, err := listener.Accept()
	listener.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "accept rsync connection: %v\n", err)
		return
	}
	defer rsync.Close()

	conn, chans, reqs, err := ssh.NewServerConn(rsync, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to handshake: %v\n", err)
		return
	}
	defer conn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if !handleChannel(newChannel, client, opts.Remote) && cancel != nil {
			cancel()
			break
		}
	}
}

func handleChannel(newChannel ssh.NewChannel, client *SSHClient, remote string) bool {
	if newChannel.ChannelType() != "session" {
		newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
		return false
	}

	if idx := strings.IndexByte(remote, ':'); idx >= 0 {
		remote = remote[:idx]
	}

	ss, err := client.NewSession(remote)
	if err != nil {
		return false
	}
	defer ss.Close()
	defer ss.Stdin.Close()

	channel, requests, err := newChannel.Accept()
	if err != nil {
		fmt.Fprintf(os.Stderr, "accept channel: %v\n", err)
		return false
	}
	defer channel.Close()

	go io.Copy(os.Stderr, channel.Stderr())

	for req := range requests {
		if req.Type == "env" {
			err = setEnv(ss, req)
		} else if req.Type == "exec" {
			err = execCmd(ss, req, channel)
		} else {
			if req.WantReply {
				err = req.Reply(false, nil)
			}
		}
		if err != nil {
			return false
		}
	}
	return true
}

func setEnv(ss *SSHSession, req *ssh.Request) error {
	var setenvRequest struct {
		Name  string
		Value string
	}
	err := ssh.Unmarshal(req.Payload, &setenvRequest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal setenv: %v\n", err)
		return err
	}
	err = ss.Run(fmt.Sprintf("export %v=%q\r", setenvRequest.Name, setenvRequest.Value))
	if err != nil {
		return err
	}
	if req.WantReply {
		req.Reply(true, nil)
	}
	return nil
}

func execCmd(ss *SSHSession, req *ssh.Request, channel ssh.Channel) error {
	reply := func(ok bool) {
		if req.WantReply {
			req.Reply(ok, nil)
		}
	}

	var execMsg struct {
		Command string
	}
	err := ssh.Unmarshal(req.Payload, &execMsg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal exec cmd: %v\n", err)
		reply(false)
		return err
	}

	c1, h1, p1, e1 := AllocProxy()
	if e1 != nil {
		reply(false)
		return e1
	}
	defer c1.Close()
	c2, h2, p2, e2 := AllocProxy()
	if e2 != nil {
		reply(false)
		return e2
	}
	defer c2.Close()

	cmd := fmt.Sprintf("nc -4 -w %v --recv-only %v %v | %v | nc -4 -w %v --send-only %v %v\r",
		Options.Wait, h1, p1, execMsg.Command, Options.Wait, h2, p2)
	if Options.Debug {
		fmt.Println(cmd)
	}
	_, err = ss.Stdin.Write([]byte(cmd))
	if err != nil {
		fmt.Fprintf(os.Stderr, "write cmd: %v\n", err)
		reply(false)
		return err
	}

	reply(true)

	wait := make(chan struct{})
	go func() {
		defer close(wait)

		_, err := io.Copy(NewRC4Writer(c1, p1), channel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local -> ssh: %v\n", err)
		}
		c1.Close()
	}()

	_, err = io.Copy(channel, NewRC4Reader(c2, p2))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh -> local: %v\n", err)
	}
	var status struct {
		Status uint32
	}
	status.Status = 0
	channel.SendRequest("exit-status", false, ssh.Marshal(&status))
	channel.Close()
	<-wait
	return nil
}

func StartRsync(ctx context.Context, address string, opts *RsyncOptions) (func() error, func() error, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "split local listen address %q: %v\n", address, err)
		return nil, nil, err
	}
	args := []string{
		"-avzhP",
		"-e", fmt.Sprintf("ssh -p %v", port),
	}
	file := ""
	if idx := strings.IndexByte(opts.Remote, ':'); idx >= 0 {
		file = opts.Remote[idx+1:]
	}
	if opts.upload {
		args = append(args, opts.Target, "127.0.0.1:"+file)
	} else {
		args = append(args, "127.0.0.1:"+file)
		if opts.Target == "" {
			args = append(args, ".")
		} else {
			args = append(args, opts.Target)
		}
	}
	cmd := exec.CommandContext(ctx, "rsync", args...)
	if Options.Debug {
		fmt.Printf("%v %v\n", Dollar, cmd.String())
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start rsync: %v\n", err)
		return nil, nil, err
	}
	return cmd.Wait, cmd.Cancel, nil
}
