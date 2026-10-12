package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

const usage = `tp — peer-to-peer file & folder transfer over WebRTC data channels

Usage:
  tp send   [-c N] [--public ip:port] [--code-file F] <path>
  tp receive [--public ip:port] [--code-file F] [outdir]
  tp version


send   offers <path> (file or folder, streamed recursively) and prints a
       session code; paste the receiver's answer code when prompted.
receive waits for a session code, then writes files into outdir (".").
       Everything lands in outdir/<name-of-transferred-path>/.

Flags:
  -c N             parallel data channels (default 8)
  --public ip:port advertise this public IPv4 endpoint instead of STUN
                   hole punching (requires a UDP port-forward to this machine;
                   lets a symmetric-NAT peer connect out to you)
  --code-file F    exchange codes via files F.offer / F.answer
                   instead of copy-paste

P2P only (Google STUN). Symmetric NATs will not connect without a relay.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "send":
		fs := flag.NewFlagSet("tp send", flag.ExitOnError)
		channels := fs.Int("c", 8, "parallel data channels")
		codeFile := fs.String("code-file", "", "exchange codes via files instead of copy-paste")
		public := fs.String("public", "", "advertise ip:port as a UDP-forwarded public endpoint")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("send: exactly one <path> required")
		}
		if *channels < 1 || *channels > 64 {
			return fmt.Errorf("channel count must be between 1 and 64")
		}
		return runSend(fs.Arg(0), *channels, *codeFile, *public)
	case "receive":
		fs := flag.NewFlagSet("tp receive", flag.ExitOnError)
		codeFile := fs.String("code-file", "", "exchange codes via files instead of copy-paste")
		public := fs.String("public", "", "advertise ip:port as a UDP-forwarded public endpoint")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 1 {
			return fmt.Errorf("receive: at most one [outdir]")
		}
		out := "."
		if fs.NArg() == 1 {
			out = fs.Arg(0)
		}
		return runReceive(out, *codeFile, *public)
	case "version":
		fmt.Println("tp", version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	return nil
}
