// Package ask lets a running script ask the user something mid-run:
//
//	name=$(lazycmd ask "Release name?" --default "$(date +%F)")
//	module=$(find . -name pom.xml | lazycmd pick "Module?")
//
// Inside lazycmd the question becomes a popup. lazycmd runs a Server on
// a Unix socket in a private (0700) folder, and gives each panel job
// LC_ASK_SOCK (the socket) and LC_ASK_TOKEN (a random token that names
// the job). The `lazycmd ask` / `lazycmd pick` client sends one request
// and waits for the answer. Outside lazycmd (no LC_ASK_SOCK, which
// includes commands run with the real terminal) the client asks on
// /dev/tty instead, and pick uses fzf when it's installed, so the same
// script works everywhere.
//
// Questions travel over the socket, never through the job's output, so
// nothing a program prints can open or imitate a popup.
package ask

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Env names the client reads.
const (
	EnvSock  = "LC_ASK_SOCK"
	EnvToken = "LC_ASK_TOKEN"
)

const (
	KindAsk  = "ask"
	KindPick = "pick"

	maxRequest = 1 << 20 // bytes; a pick list bigger than this is a mistake
)

// Option is one choice of a pick: Value is printed when chosen, Label
// (if set) is shown instead.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Request is one question, sent by the client as a single JSON line.
type Request struct {
	Token   string   `json:"token"`
	Kind    string   `json:"kind"`
	Prompt  string   `json:"prompt"`
	Default string   `json:"default,omitempty"`
	Secret  bool     `json:"secret,omitempty"`
	Multi   bool     `json:"multi,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// Response is the answer, sent back as a single JSON line. Values holds
// a multi pick's choices; Value everything else.
type Response struct {
	Value     string   `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (r Request) validate() error {
	switch r.Kind {
	case KindAsk:
		if len(r.Options) > 0 || r.Multi {
			return errors.New("ask takes no options")
		}
	case KindPick:
		if len(r.Options) == 0 {
			return errors.New("pick needs at least one option")
		}
		if r.Secret {
			return errors.New("pick can't be secret")
		}
	default:
		return fmt.Errorf("unknown kind %q", r.Kind)
	}
	return nil
}

// Pending is a request waiting for the user, as the UI sees it.
type Pending struct {
	Req   Request
	JobID string

	reply chan Response
	once  sync.Once
	gone  chan struct{} // closed when the client hung up
}

// Answer replies to the client. Only the first call counts.
func (p *Pending) Answer(r Response) {
	p.once.Do(func() { p.reply <- r })
}

// Gone reports whether the client has hung up (the script was stopped),
// so the UI can drop the question.
func (p *Pending) Gone() bool {
	select {
	case <-p.gone:
		return true
	default:
		return false
	}
}

// Server accepts questions from jobs and hands them to the UI on
// Requests.
type Server struct {
	dir  string
	path string
	ln   net.Listener

	mu     sync.Mutex
	tokens map[string]string // token -> job id

	// Requests delivers questions to the UI. The UI must Answer each one.
	Requests chan *Pending
}

// NewServer listens on a socket in a new private folder under baseDir
// (or the system temp folder, if baseDir would make the socket path too
// long for the OS limit of about 104 bytes).
func NewServer(baseDir string) (*Server, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(baseDir, "ask-")
	if err == nil && len(filepath.Join(dir, "s")) > 100 {
		os.RemoveAll(dir)
		err = errors.New("path too long")
	}
	if err != nil {
		if dir, err = os.MkdirTemp("", "lazycmd-ask-"); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil { // MkdirTemp does this, but say it
		os.RemoveAll(dir)
		return nil, err
	}
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	s := &Server{dir: dir, path: path, ln: ln, tokens: map[string]string{}, Requests: make(chan *Pending, 16)}
	go s.serve()
	return s, nil
}

// Path is the socket, for LC_ASK_SOCK.
func (s *Server) Path() string { return s.path }

// Register returns a new token for a job, for LC_ASK_TOKEN.
func (s *Server) Register(jobID string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = jobID
	s.mu.Unlock()
	return tok
}

// Unregister forgets a job's token once it has finished.
func (s *Server) Unregister(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// Close stops listening and removes the socket's folder.
func (s *Server) Close() error {
	err := s.ln.Close()
	os.RemoveAll(s.dir)
	return err
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // closed
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	reply := func(r Response) {
		data, _ := json.Marshal(r)
		_, _ = conn.Write(append(data, '\n'))
	}

	// The request itself must arrive promptly; the answer can take as
	// long as the user does.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(conn, maxRequest)).ReadBytes('\n')
	if err != nil {
		reply(Response{Error: "bad request: " + err.Error()})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		reply(Response{Error: "bad request: " + err.Error()})
		return
	}
	s.mu.Lock()
	jobID, ok := s.tokens[req.Token]
	s.mu.Unlock()
	if !ok {
		reply(Response{Error: "unknown token: only jobs lazycmd started can ask"})
		return
	}
	if err := req.validate(); err != nil {
		reply(Response{Error: err.Error()})
		return
	}

	p := &Pending{Req: req, JobID: jobID, reply: make(chan Response, 1), gone: make(chan struct{})}
	// Notice the client hanging up (the script was stopped) while it
	// waits: it sends nothing more, so any read returning means gone.
	go func() {
		_, _ = conn.Read(make([]byte, 1))
		close(p.gone)
		p.Answer(Response{Cancelled: true})
	}()

	select {
	case s.Requests <- p:
	default:
		reply(Response{Error: "too many questions waiting"})
		return
	}
	reply(<-p.reply)
}

// Send is the client side: it sends req to the socket and waits for the
// answer.
func Send(sock string, req Request) (Response, error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Response{}, fmt.Errorf("no answer from lazycmd: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
