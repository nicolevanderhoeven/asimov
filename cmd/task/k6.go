package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	isWindows      = runtime.GOOS == "windows"
	defaultBaseURL = "http://localhost:8080"
	k6InstallURL   = "https://grafana.com/docs/k6/latest/set-up/install-k6/"
)

var serverLog = filepath.Join("logs", "server.log")

// chdirRoot moves to the repository root, found from the working directory
// upwards, so targets behave the same wherever they are started from.
func chdirRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go-game", "cmd", "enterprise")); err == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return errors.New("run this from inside the asimov repository")
		}
		dir = parent
	}
}

// ensureEnv creates .env from env.example the first time, then stops so it
// can be filled in.
func ensureEnv(stdout io.Writer) error {
	if _, err := os.Stat(".env"); err == nil {
		return nil
	}
	b, err := os.ReadFile("env.example")
	if err != nil {
		return err
	}
	if err := os.WriteFile(".env", b, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Created .env from env.example. Fill it in (see docs/grafana-cloud-setup.md), then run the doctor target.")
	return exitError{1}
}

// child runs a command in dir with the terminal attached, and returns its
// exit code as an exitError. Ctrl-C is left to the child, which receives it
// from the terminal too, so the exit code still comes back.
func (a *app) child(dir string, env environ, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.stdout, a.stderr
	_, stop := interrupts()
	defer stop()
	return exitCode(cmd.Run())
}

func exitCode(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitError{ee.ExitCode()}
	}
	return err
}

// game runs the game from source, from go-game so that its default ../.env
// is the repository's .env.
func (a *app) game(extra []string, flags ...string) error {
	args := append([]string{"run", "./cmd/enterprise"}, flags...)
	return a.child("go-game", a.env, "go", append(args, extra...)...)
}

// unit vets and tests the game, then the task runner itself. The race
// detector needs cgo and a C compiler, which many Windows setups lack, so it
// is used only where it can work.
func (a *app) unit() error {
	test := []string{"test", "./..."}
	if raceSupported() {
		test = []string{"test", "-race", "./..."}
	} else {
		fmt.Fprintln(a.stdout, "Skipping -race: it needs cgo and a C compiler on this platform.")
	}
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{"go-game", []string{"vet", "./..."}},
		{"go-game", test},
		{".", []string{"vet", "./..."}},
		{".", []string{"test", "./..."}},
	} {
		if err := a.child(step.dir, a.env, "go", step.args...); err != nil {
			return err
		}
	}
	return nil
}

// raceSupported reports whether go test -race can work here.
func raceSupported() bool {
	out, err := exec.Command("go", "env", "CGO_ENABLED", "CC", "GOOS", "GOARCH").Output()
	if err != nil {
		return false
	}
	f := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	if len(f) < 4 || f[0] != "1" {
		return false
	}
	cc := strings.Fields(f[1])
	if len(cc) == 0 {
		return false
	}
	if _, err := exec.LookPath(cc[0]); err != nil {
		return false
	}
	switch f[2] + "/" + f[3] {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "freebsd/amd64":
		return true
	}
	return false
}

func needK6() error {
	if _, err := exec.LookPath("k6"); err != nil {
		return fmt.Errorf("k6 is not installed: %s", k6InstallURL)
	}
	return nil
}

// k6 runs a k6 script with the settings from .env, against the game server
// at BASE_URL (default http://localhost:8080). If nothing is listening
// there, it builds and starts a server for the run and stops it afterwards;
// the server's output goes to logs/server.log. With K6_CLOUD=1, the test
// still runs here but streams its results to Grafana Cloud k6.
func (a *app) k6(args []string) error { return a.k6With(args, false) }

// k6With is k6, and with mock set it runs the test free: the game server
// plays its mock game master (--llm mock) and a local mock judge answers the
// script's Claude calls. See mock.go.
func (a *app) k6With(args []string, mock bool) error {
	if err := needK6(); err != nil {
		return err
	}
	// k6 reads its settings from its own environment, not from .env.
	env, err := a.env.withDotEnv(".env")
	if err != nil {
		return err
	}
	env = append(env, a.override...)
	defaultURL := defaultBaseURL
	var serverFlags []string
	sig, stop := interrupts()
	defer stop()
	if mock {
		var judgeAddr string
		env, defaultURL, serverFlags, judgeAddr = mockSettings(env)
		judge, err := a.startMockJudge(env, judgeAddr, sig)
		if err != nil {
			return err
		}
		if judge != nil {
			defer judge.stop()
		}
	}
	baseURL, _ := env.Get("BASE_URL")
	if baseURL == "" {
		baseURL = defaultURL
	}
	env = env.Set("BASE_URL", baseURL)

	var srv *server
	if !up(baseURL) {
		if !startable(baseURL) {
			return fmt.Errorf("No game server is answering at %s.", baseURL)
		}
		fmt.Fprintf(a.stdout, "Starting the game server for this run (output in %s)...\n", serverLog)
		if srv, err = a.startServer(env, baseURL, serverFlags, sig); err != nil {
			return err
		}
		defer srv.stop()
	}

	cmdArgs := append([]string{"run"}, args...)
	if cloud, _ := env.Get("K6_CLOUD"); cloud == "1" {
		// Local execution reaches the game on localhost. Without the archive
		// upload, the script and the environment it reads (API keys) stay on
		// this machine.
		cmdArgs = append([]string{"cloud", "run", "--local-execution", "--no-archive-upload", "--include-system-env-vars"}, args...)
	}
	cmd := exec.Command("k6", cmdArgs...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.stdout, a.stderr
	err = exitCode(cmd.Run())
	if err != nil && srv != nil && len(sig) == 0 && !mock {
		srv.explain(a)
	}
	return err
}

// startable reports whether a run may start its own game server for baseURL:
// only on this machine, on the port the runner uses.
func startable(baseURL string) bool {
	switch strings.TrimRight(baseURL, "/") {
	case defaultBaseURL, "http://127.0.0.1:8080", mockBaseURL, "http://127.0.0.1:8081":
		return true
	}
	return false
}

// up reports whether anything answers at baseURL. Any HTTP answer, even a
// 404, means a server is up.
func up(baseURL string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(strings.TrimRight(baseURL, "/") + "/session/ping")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// server is a game server started for one k6 run.
type server struct {
	cmd  *exec.Cmd
	log  *os.File
	done chan struct{} // closed when the process exits
}

// build compiles one of the game's commands into go-game and returns the
// path of the program.
func (a *app) build(env environ, name, what string) (string, error) {
	exe := name
	if isWindows {
		exe += ".exe"
	}
	build := exec.Command("go", "build", "-o", exe, "./cmd/"+name)
	build.Dir = "go-game"
	build.Env = env
	build.Stdout, build.Stderr = a.stdout, a.stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("building %s failed: %w", what, err)
	}
	return filepath.Abs(filepath.Join("go-game", exe))
}

// spawn starts a built program in go-game, with its output in logPath.
func spawn(env environ, path, logPath string, args ...string) (*server, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = "go-game"
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	s := &server{cmd: cmd, log: logFile, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

// startServer starts the game server for baseURL. flags are added to the ones
// that serve it there.
func (a *app) startServer(env environ, baseURL string, flags []string, sig <-chan os.Signal) (*server, error) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, err
	}
	path, err := a.build(env, "enterprise", "the game server")
	if err != nil {
		return nil, err
	}
	addr := ":8080"
	if u, err := url.Parse(baseURL); err == nil && u.Port() != "" {
		addr = ":" + u.Port()
	}
	s, err := spawn(env, path, serverLog, append([]string{"--serve", "--addr", addr}, flags...)...)
	if err != nil {
		return nil, err
	}

	for i := 0; i < 30; i++ {
		if up(baseURL) {
			return s, nil
		}
		select {
		case <-s.done:
			s.log.Close()
			return nil, fmt.Errorf("The game server exited; see %s:\n%s", serverLog, tail(serverLog, 5))
		case <-sig:
			s.stop()
			return nil, exitError{130}
		case <-time.After(time.Second):
		}
	}
	s.stop()
	return nil, fmt.Errorf("The game server didn't start within 30s; see %s.", serverLog)
}

// stop shuts the server down: Ctrl-C where that can be sent, so it flushes
// its telemetry, and a kill where it can't (Windows) or if it won't go.
func (s *server) stop() {
	select {
	case <-s.done:
	default:
		if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
			s.cmd.Process.Kill()
		}
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			s.cmd.Process.Kill()
			<-s.done
		}
	}
	s.log.Close()
}

// explain says where to look after a failed run, and shows the end of the
// server's log if the server itself died during it.
func (s *server) explain(a *app) {
	select {
	case <-s.done:
		fmt.Fprintf(a.stderr, "The game server exited during the run; the end of %s:\n%s\n", serverLog, tail(serverLog, 20))
	default:
		fmt.Fprintf(a.stderr, "The game server's output is in %s.\n", serverLog)
	}
}

// tail returns the last n lines of the file.
func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
