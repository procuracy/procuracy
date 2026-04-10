// Command procuracy is the CLI for hiring, running, and firing AI contractors.
//
// The full command surface is documented in the README. This file is the
// dispatch shim — every subcommand is a stub except `validate`, which
// actually runs the manifest parser. Wiring real behavior behind the rest
// is the next step.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/manifest"
)

const usage = `procuracy — hire AI contractors into your engineering team

Usage:
  procuracy <command> [arguments]

Commands:
  hire <path>        Provision identities and accounts for a contractor manifest
  start <path>       Start the runtime loop for a contractor
  pause <name>       Suspend a contractor without revoking credentials
  update <path>      Hot-reload a contractor manifest
  logs <name>        Tail the audit log for a contractor
  report <name>      Print a weekly performance summary
  fire <name>        Revoke all credentials and archive accounts
  auth <provider>    Authenticate to an integration (github|slack|linear|anthropic)
  run <dir>          Run a contractor's handler with trust guardrails
  init               Scaffold a new contractor interactively
  demo               Generate a sample contractor + audit log for a hands-on trial
  validate <path>    Parse and validate a procuracy.yaml manifest
  verify <path>      Verify a procuracy audit log JSONL file (chain integrity)
  version            Print the procuracy version

Run 'procuracy <command> -h' for command-specific help.
See https://github.com/procuracy/procuracy for the full documentation.
`

// version is overwritten at build time via -ldflags.
var version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "validate":
		return cmdValidate(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	case "run":
		return cmdRun(rest, stdout, stderr)
	case "demo":
		return cmdDemo(stdout, stderr)
	case "init":
		return cmdInit(rest, os.Stdin, stdout, stderr)
	case "hire", "start", "pause", "update", "logs", "report", "fire", "auth":
		fmt.Fprintf(stderr, "procuracy %s: not implemented yet (tracked in docs/roadmap.md)\n", cmd)
		return 64
	default:
		fmt.Fprintf(stderr, "procuracy: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: procuracy validate <path-to-procuracy.yaml>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)
	m, err := manifest.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %s (%d trigger(s), %d handler(s))\n", m.Name, len(m.Triggers), len(m.Handlers))
	for _, w := range m.Warnings() {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	return 0
}

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: procuracy verify <path-to-audit.jsonl>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	count, err := audit.VerifyFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %d entries verified\n", count)
	return 0
}
