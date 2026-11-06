// Package envfile loads KEY=VALUE pairs from a .env file into the process
// environment.
//
// The backend reads all of its configuration through os.Getenv, which sees only
// the shell environment. That means eight variables have to be exported by hand
// in every new terminal. This package closes that gap without pulling in a
// dependency.
//
// The central rule: a variable already present in the environment is never
// overwritten. The shell always wins over the file, so a one-off
//
//	MCDVOICE_LIVE=0 go run ./cmd/api
//
// still forces simulation even when the file says otherwise. That ordering
// matters for a flag whose "on" state performs irreversible real-world
// submissions.
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultName is the file looked for when no explicit path is given.
const DefaultName = ".env"

// maxParents bounds the upward search. `go run ./cmd/api` runs with the
// working directory wherever the user invoked it, so the file is found by
// walking up — but only far enough to escape cmd/<binary>, never to the
// filesystem root, which could pick up an unrelated .env.
const maxParents = 4

// Result describes what Load did, so the caller can log it. Silent
// configuration loading is miserable to debug: "which file did it read" is the
// first question when a value does not take effect.
type Result struct {
	// Path is the file that was read, empty when none was found.
	Path string
	// Applied names the variables taken from the file.
	Applied []string
	// Skipped names variables present in the file but already set in the
	// environment, so the file's value was ignored.
	Skipped []string
}

// Summary renders a one-line description for a log.
func (r Result) Summary() string {
	if r.Path == "" {
		return "no .env file found; using the shell environment only"
	}
	s := fmt.Sprintf("loaded %d variable(s) from %s", len(r.Applied), r.Path)
	if len(r.Skipped) > 0 {
		s += fmt.Sprintf("; %d already set in the shell and left alone (%s)",
			len(r.Skipped), strings.Join(r.Skipped, ", "))
	}
	return s
}

// Load finds a .env file near the working directory and applies it.
//
// A missing file is not an error: every variable has either a default or a
// clear error at point of use, and requiring the file would break deployments
// that inject real environment variables instead.
func Load() (Result, error) {
	path, err := find(DefaultName)
	if err != nil {
		return Result{}, err
	}
	if path == "" {
		return Result{}, nil
	}
	return LoadFile(path)
}

// find walks up from the working directory looking for name.
func find(name string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determining working directory: %w", err)
	}
	for i := 0; i <= maxParents; i++ {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the filesystem root
		}
		dir = parent
	}
	return "", nil
}

// LoadFile applies a specific file.
func LoadFile(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	result := Result{Path: path}
	scanner := bufio.NewScanner(f)
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		key, value, ok, err := parseLine(scanner.Text())
		if err != nil {
			return result, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if !ok {
			continue // blank or comment
		}
		// The shell wins. os.LookupEnv rather than os.Getenv, so a variable
		// deliberately set to empty still counts as set.
		if _, present := os.LookupEnv(key); present {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return result, fmt.Errorf("%s:%d: setting %s: %w", path, lineNo, key, err)
		}
		result.Applied = append(result.Applied, key)
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("reading %s: %w", path, err)
	}
	return result, nil
}

// parseLine splits one line into a key and value.
//
// Supported, because real .env files in the wild contain all of it:
//
//	KEY=value
//	export KEY=value          # the leading `export` is tolerated
//	KEY="quoted value"        # escapes \n \t \\ \" are expanded
//	KEY='literal value'       # no escape processing
//	KEY=value # trailing       # comment stripped only when unquoted
//	# comment
//	(blank)
//
// ok is false for blank and comment lines.
func parseLine(line string) (key, value string, ok bool, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false, nil
	}

	// `export FOO=bar` is accepted so a file can be pasted straight from the
	// shell commands in the docs.
	trimmed = strings.TrimPrefix(trimmed, "export ")
	trimmed = strings.TrimSpace(trimmed)

	eq := strings.IndexByte(trimmed, '=')
	if eq < 0 {
		return "", "", false, fmt.Errorf("expected KEY=VALUE, got %q", line)
	}

	key = strings.TrimSpace(trimmed[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("empty key in %q", line)
	}
	if err := validateKey(key); err != nil {
		return "", "", false, err
	}

	raw := strings.TrimSpace(trimmed[eq+1:])
	value, err = parseValue(raw)
	if err != nil {
		return "", "", false, err
	}
	return key, value, true, nil
}

// validateKey rejects names a shell could not export, which usually means the
// line is malformed rather than that the user wanted an exotic name.
func validateKey(key string) error {
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return fmt.Errorf("invalid character %q in key %q", r, key)
		}
	}
	return nil
}

// parseValue handles quoting and trailing comments.
func parseValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	switch raw[0] {
	case '"':
		body, rest, err := splitQuoted(raw, '"')
		if err != nil {
			return "", err
		}
		if err := checkTrailer(rest); err != nil {
			return "", err
		}
		return unescape(body), nil
	case '\'':
		// Single quotes are literal, matching shell semantics: no escape
		// processing, so a Windows path or a password full of backslashes
		// survives intact.
		body, rest, err := splitQuoted(raw, '\'')
		if err != nil {
			return "", err
		}
		if err := checkTrailer(rest); err != nil {
			return "", err
		}
		return body, nil
	}

	// Unquoted: a ` #` begins a comment. The space is required, so a value like
	// a URL fragment or a password containing '#' is not silently truncated.
	if idx := strings.Index(raw, " #"); idx >= 0 {
		raw = raw[:idx]
	} else if idx := strings.Index(raw, "\t#"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw), nil
}

// splitQuoted returns the text inside the opening quote and whatever follows
// the closing one.
func splitQuoted(raw string, quote byte) (body, rest string, err error) {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '\\' && quote == '"' {
			i++ // skip the escaped character
			continue
		}
		if raw[i] == quote {
			return raw[1:i], raw[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated %c quote in value", quote)
}

// checkTrailer allows only whitespace or a comment after a closing quote.
func checkTrailer(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return fmt.Errorf("unexpected text after closing quote: %q", rest)
}

// unescape expands the escapes a double-quoted value may contain. Notably \n,
// so a multi-line value such as a PEM key can live on one line.
func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		default:
			// Unknown escape is kept verbatim rather than silently eaten.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
