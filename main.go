package main

import (
	"context"
	"os"

	"github.com/eachain/flagrouter"
)

type RunOptions struct {
	Jumper string `long:"jumper" required:"true" desc:"jump server host"`
	User   string `long:"user" desc:"ssh user, default is $USER"`
	SSHKey string `long:"ssh-key" desc:"ssh private key file (default: \"$HOME/.ssh/id_rsa\")"`
	Proxy  string `long:"proxy" required:"true" desc:"proxy server tcp4 address"`
	Wait   int64  `short:"w" long:"wait" dft:"3" desc:"jumper/proxy connect timeout seconds"`
	Debug  bool   `long:"debug" desc:"output all cmd running info"`
}

var Options *RunOptions

func main() {
	r := flagrouter.Cmdline("implement rsync and tcp forward via jumper and proxy.")

	r.Use(func(opts *RunOptions) {
		if opts.User == "" {
			opts.User = os.Getenv("USER")
		}
		Options = opts
	})

	r.HandleGroup("rsync", "rsync file between local and remote", Rsync, "r")
	r.HandleGroup("forward", "forward remote tcp port to local", TCPForward, "f")

	r.RunCmdline(context.Background())
}
