package ask

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Exit codes, as fzf uses them.
const (
	ExitOK        = 0
	ExitCancelled = 1
	ExitUsage     = 2
)

const usage = `usage:
  lazycmd ask PROMPT [--default VALUE] [--secret]
  lazycmd pick PROMPT [--multi]      (options on stdin, one per line: VALUE or VALUE<TAB>LABEL)

Prints the answer (pick --multi: one choice per line). Exits 1 if cancelled.
Inside lazycmd this opens a popup; elsewhere it asks on the terminal
(pick uses fzf when it's installed).`

// Swappable for tests.
var (
	openTTY = func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	// readSecret reads a line with echo off.
	readSecret = func(tty io.ReadWriteCloser) (string, error) {
		f, ok := tty.(*os.File)
		if !ok {
			return readLine(tty)
		}
		b, err := term.ReadPassword(f.Fd())
		fmt.Fprintln(tty)
		return string(b), err
	}
	lookFzf = func() (string, error) { return exec.LookPath("fzf") }
)

// Main runs `lazycmd ask|pick args...` and returns the exit code.
// stdinIsTTY says whether stdin is a terminal (pick needs it piped).
func Main(kind string, args []string, stdin io.Reader, stdinIsTTY bool, stdout, stderr io.Writer) int {
	req, err := parseArgs(kind, args)
	if err != nil {
		fmt.Fprintf(stderr, "lazycmd %s: %v\n\n%s\n", kind, err, usage)
		return ExitUsage
	}
	if kind == KindPick {
		if stdinIsTTY {
			fmt.Fprintf(stderr, "lazycmd pick: pipe the options in, one per line\n\n%s\n", usage)
			return ExitUsage
		}
		opts, err := readOptions(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "lazycmd pick: %v\n", err)
			return ExitUsage
		}
		if len(opts) == 0 {
			fmt.Fprintln(stderr, "lazycmd pick: no options on stdin")
			return ExitCancelled
		}
		req.Options = opts
	}

	var resp Response
	if sock := os.Getenv(EnvSock); sock != "" {
		req.Token = os.Getenv(EnvToken)
		resp, err = Send(sock, req)
	} else {
		resp, err = local(req)
	}
	if err != nil {
		fmt.Fprintf(stderr, "lazycmd %s: %v\n", kind, err)
		return ExitUsage
	}
	switch {
	case resp.Error != "":
		fmt.Fprintf(stderr, "lazycmd %s: %s\n", kind, resp.Error)
		return ExitUsage
	case resp.Cancelled:
		return ExitCancelled
	case req.Multi:
		for _, v := range resp.Values {
			fmt.Fprintln(stdout, v)
		}
	default:
		fmt.Fprintln(stdout, resp.Value)
	}
	return ExitOK
}

func parseArgs(kind string, args []string) (Request, error) {
	req := Request{Kind: kind}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--secret" && kind == KindAsk:
			req.Secret = true
		case a == "--multi" && kind == KindPick:
			req.Multi = true
		case a == "--default" && kind == KindAsk:
			if i+1 >= len(args) {
				return req, errors.New("--default needs a value")
			}
			i++
			req.Default = args[i]
		case strings.HasPrefix(a, "--default=") && kind == KindAsk:
			req.Default = strings.TrimPrefix(a, "--default=")
		case a == "-h" || a == "--help":
			return req, errors.New("help")
		case strings.HasPrefix(a, "-") && a != "-":
			return req, fmt.Errorf("unknown flag %s", a)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return req, errors.New("give exactly one PROMPT")
	}
	req.Prompt = positional[0]
	return req, nil
}

func readOptions(r io.Reader) ([]Option, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRequest))
	if err != nil {
		return nil, err
	}
	var opts []Option
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		value, label, _ := strings.Cut(line, "\t")
		value, label = strings.TrimSpace(value), strings.TrimSpace(label)
		if value != "" {
			opts = append(opts, Option{Value: value, Label: label})
		}
	}
	return opts, nil
}

// local answers req on the terminal, for scripts run outside lazycmd.
func local(req Request) (Response, error) {
	if req.Kind == KindPick {
		if fzf, err := lookFzf(); err == nil {
			return pickFzf(fzf, req)
		}
	}
	tty, err := openTTY()
	if err != nil {
		return Response{}, fmt.Errorf("no terminal to ask on: %v", err)
	}
	defer tty.Close()
	if req.Kind == KindPick {
		return pickMenu(tty, req)
	}
	return askTTY(tty, req)
}

func askTTY(tty io.ReadWriteCloser, req Request) (Response, error) {
	prompt := req.Prompt
	if req.Default != "" && !req.Secret {
		prompt += " [" + req.Default + "]"
	}
	fmt.Fprint(tty, prompt+" ")
	var line string
	var err error
	if req.Secret {
		line, err = readSecret(tty)
	} else {
		line, err = readLine(tty)
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Response{Cancelled: true}, nil
		}
		return Response{}, err
	}
	if line == "" {
		line = req.Default
	}
	return Response{Value: line}, nil
}

// pickMenu is the fallback without fzf: a numbered list.
func pickMenu(tty io.ReadWriteCloser, req Request) (Response, error) {
	for i, o := range req.Options {
		label := o.Label
		if label == "" {
			label = o.Value
		}
		fmt.Fprintf(tty, "%3d) %s\n", i+1, label)
	}
	hint := "number"
	if req.Multi {
		hint = "numbers, separated by spaces"
	}
	r := bufio.NewReader(tty)
	for {
		fmt.Fprintf(tty, "%s (%s, empty to cancel): ", req.Prompt, hint)
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return Response{Cancelled: true}, nil
		}
		var picked []string
		ok := true
		for _, f := range strings.Fields(line) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(req.Options) {
				ok = false
				break
			}
			picked = append(picked, req.Options[n-1].Value)
		}
		if ok && (req.Multi || len(picked) == 1) {
			if req.Multi {
				return Response{Values: picked}, nil
			}
			return Response{Value: picked[0]}, nil
		}
		fmt.Fprintln(tty, "  not a valid choice")
		if err != nil {
			return Response{Cancelled: true}, nil
		}
	}
}

// pickFzf runs fzf for a pick outside lazycmd. When any option has a
// label, every line is VALUE<TAB>LABEL and fzf shows only the label.
func pickFzf(fzf string, req Request) (Response, error) {
	labelled := false
	for _, o := range req.Options {
		if o.Label != "" {
			labelled = true
		}
	}
	var in bytes.Buffer
	for _, o := range req.Options {
		if labelled {
			label := o.Label
			if label == "" {
				label = o.Value
			}
			fmt.Fprintf(&in, "%s\t%s\n", o.Value, label)
		} else {
			fmt.Fprintln(&in, o.Value)
		}
	}
	args := []string{"--prompt", req.Prompt + " "}
	if labelled {
		args = append(args, "--delimiter", "\t", "--with-nth", "2..")
	}
	if req.Multi {
		args = append(args, "--multi")
	}
	cmd := exec.Command(fzf, args...)
	cmd.Stdin = &in
	cmd.Stderr = os.Stderr // fzf draws on /dev/tty itself
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 1 || ee.ExitCode() == 130) {
			return Response{Cancelled: true}, nil // no match, or esc/ctrl-c
		}
		return Response{}, err
	}
	var picked []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if v, _, _ := strings.Cut(line, "\t"); v != "" {
			picked = append(picked, v)
		}
	}
	if len(picked) == 0 {
		return Response{Cancelled: true}, nil
	}
	if req.Multi {
		return Response{Values: picked}, nil
	}
	return Response{Value: picked[0]}, nil
}

func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimRight(b.String(), "\r"), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if b.Len() > 0 && errors.Is(err, io.EOF) {
				return b.String(), nil
			}
			return "", err
		}
	}
}
