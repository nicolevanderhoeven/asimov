package main

import (
	"bufio"
	"io"
	"os"
	"runtime"
	"strings"
)

// environ is a list of KEY=value strings, like os.Environ. Setting a key
// appends to the list, because exec.Cmd uses the last value of a duplicate
// key.
type environ []string

func sameKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Get returns the value of key, and whether it is set.
func (e environ) Get(key string) (string, bool) {
	for i := len(e) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(e[i], "="); ok && sameKey(k, key) {
			return v, true
		}
	}
	return "", false
}

// Set returns e with key set to value.
func (e environ) Set(key, value string) environ {
	return append(e[:len(e):len(e)], key+"="+value)
}

type envVar struct{ key, value string }

// parseEnv reads the KEY=value lines the k6 runner passes on from .env. It
// understands what the shell would have made of env.example: comments,
// blank lines, an optional "export", single or double quotes, and a trailing
// " # comment" on an unquoted value. Windows line endings and a byte order
// mark are ignored.
func parseEnv(r io.Reader) ([]envVar, error) {
	var vars []envVar
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			continue
		}
		vars = append(vars, envVar{key, unquote(strings.TrimSpace(value))})
	}
	return vars, sc.Err()
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func unquote(v string) string {
	switch {
	case strings.HasPrefix(v, "'"):
		if end := strings.IndexByte(v[1:], '\''); end >= 0 {
			return v[1 : 1+end]
		}
	case strings.HasPrefix(v, `"`):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			switch c := v[i]; {
			case c == '"':
				return b.String()
			case c == '\\' && i+1 < len(v) && (v[i+1] == '"' || v[i+1] == '\\'):
				i++
				b.WriteByte(v[i])
			default:
				b.WriteByte(c)
			}
		}
		return b.String()
	}
	// An unquoted value ends at whitespace followed by #.
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// withDotEnv returns e with the values from the .env file at path applied
// over it, so .env wins over the shell as it always has for the k6 runs. A
// missing file changes nothing. Empty values are skipped: env.example ships
// every key blank, and a blank line must not erase a key set in the shell.
func (e environ) withDotEnv(path string) (environ, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	defer f.Close()
	vars, err := parseEnv(f)
	if err != nil {
		return e, err
	}
	for _, v := range vars {
		if v.value != "" {
			e = e.Set(v.key, v.value)
		}
	}
	return e, nil
}
