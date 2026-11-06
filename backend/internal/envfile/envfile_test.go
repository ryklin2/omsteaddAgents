package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
		wantErr   string
	}{
		{name: "simple", line: "FOO=bar", wantKey: "FOO", wantValue: "bar", wantOK: true},
		{name: "spaces around equals", line: "FOO = bar", wantKey: "FOO", wantValue: "bar", wantOK: true},
		{name: "export prefix", line: "export FOO=bar", wantKey: "FOO", wantValue: "bar", wantOK: true},
		{name: "empty value", line: "FOO=", wantKey: "FOO", wantValue: "", wantOK: true},
		{name: "blank line", line: "", wantOK: false},
		{name: "whitespace only", line: "   ", wantOK: false},
		{name: "comment", line: "# a comment", wantOK: false},
		{name: "indented comment", line: "   # a comment", wantOK: false},

		{name: "double quoted", line: `FOO="bar baz"`, wantKey: "FOO", wantValue: "bar baz", wantOK: true},
		{name: "single quoted", line: `FOO='bar baz'`, wantKey: "FOO", wantValue: "bar baz", wantOK: true},
		{name: "double quoted expands newline", line: `FOO="a\nb"`, wantKey: "FOO", wantValue: "a\nb", wantOK: true},
		{name: "single quoted is literal", line: `FOO='a\nb'`, wantKey: "FOO", wantValue: `a\nb`, wantOK: true},
		{name: "escaped quote", line: `FOO="say \"hi\""`, wantKey: "FOO", wantValue: `say "hi"`, wantOK: true},

		{
			name:      "trailing comment stripped when unquoted",
			line:      "FOO=bar # trailing",
			wantKey:   "FOO",
			wantValue: "bar",
			wantOK:    true,
		},
		{
			// The space before # is required precisely so this works: an API key
			// or URL fragment containing # must survive.
			name:      "hash inside unquoted value is kept",
			line:      "FOO=abc#def",
			wantKey:   "FOO",
			wantValue: "abc#def",
			wantOK:    true,
		},
		{
			name:      "hash inside quoted value is kept",
			line:      `FOO="abc # def"`,
			wantKey:   "FOO",
			wantValue: "abc # def",
			wantOK:    true,
		},
		{
			name:      "comment after closing quote",
			line:      `FOO="bar"  # note`,
			wantKey:   "FOO",
			wantValue: "bar",
			wantOK:    true,
		},
		{name: "value with equals signs", line: "FOO=a=b=c", wantKey: "FOO", wantValue: "a=b=c", wantOK: true},
		{name: "underscore and digits in key", line: "FOO_BAR2=x", wantKey: "FOO_BAR2", wantValue: "x", wantOK: true},

		{name: "no equals", line: "JUST_A_WORD", wantErr: "expected KEY=VALUE"},
		{name: "empty key", line: "=value", wantErr: "empty key"},
		{name: "key starting with digit", line: "2FOO=bar", wantErr: "invalid character"},
		{name: "key with dash", line: "FOO-BAR=baz", wantErr: "invalid character"},
		{name: "unterminated double quote", line: `FOO="bar`, wantErr: "unterminated"},
		{name: "unterminated single quote", line: `FOO='bar`, wantErr: "unterminated"},
		{name: "junk after closing quote", line: `FOO="bar" junk`, wantErr: "unexpected text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, value, ok, err := parseLine(tc.line)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseLine(%q) = (%q, %q, %v, nil), want error containing %q", tc.line, key, value, ok, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLine(%q) unexpected error: %v", tc.line, err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			if value != tc.wantValue {
				t.Errorf("value = %q, want %q", value, tc.wantValue)
			}
		})
	}
}

// TestShellWinsOverFile is the guarantee that matters most: MCDVOICE_LIVE=0 on
// the command line must beat MCDVOICE_LIVE=1 in the file, or a developer cannot
// force simulation mode.
func TestShellWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "MCDVOICE_LIVE=1\nGOOGLE_API_KEY=from-file\nGEMINI_MODEL=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Already set in the "shell".
	t.Setenv("MCDVOICE_LIVE", "0")
	t.Setenv("GOOGLE_API_KEY", "from-shell")
	// GEMINI_MODEL deliberately left unset, then cleared so the file can win.
	t.Setenv("GEMINI_MODEL", "placeholder")
	if err := os.Unsetenv("GEMINI_MODEL"); err != nil {
		t.Fatal(err)
	}

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() failed: %v", err)
	}

	if got := os.Getenv("MCDVOICE_LIVE"); got != "0" {
		t.Errorf("MCDVOICE_LIVE = %q, want 0 — the file overrode the shell, which would force live submissions", got)
	}
	if got := os.Getenv("GOOGLE_API_KEY"); got != "from-shell" {
		t.Errorf("GOOGLE_API_KEY = %q, want from-shell", got)
	}
	if got := os.Getenv("GEMINI_MODEL"); got != "from-file" {
		t.Errorf("GEMINI_MODEL = %q, want from-file (unset in shell, so the file should apply)", got)
	}

	if len(res.Applied) != 1 || res.Applied[0] != "GEMINI_MODEL" {
		t.Errorf("Applied = %v, want [GEMINI_MODEL]", res.Applied)
	}
	if len(res.Skipped) != 2 {
		t.Errorf("Skipped = %v, want 2 entries", res.Skipped)
	}
}

// TestEmptyShellValueCountsAsSet covers the LookupEnv-vs-Getenv distinction: a
// variable explicitly set to "" is set, and the file must not refill it.
func TestEmptyShellValueCountsAsSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("SOME_FLAG=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOME_FLAG", "")

	if _, err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() failed: %v", err)
	}
	if got := os.Getenv("SOME_FLAG"); got != "" {
		t.Errorf("SOME_FLAG = %q, want it to stay empty; an explicit empty value is still a value", got)
	}
}

func TestLoadFileReportsLineNumberOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "GOOD=1\n\n# comment\nBROKEN LINE\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("LoadFile() succeeded on a malformed file, want an error")
	}
	if !strings.Contains(err.Error(), ":4:") {
		t.Errorf("error = %q, want it to name line 4", err)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	res, err := Load()
	if err != nil {
		t.Fatalf("Load() with no .env present failed: %v", err)
	}
	if res.Path != "" {
		t.Errorf("Path = %q, want empty when no file exists", res.Path)
	}
	if !strings.Contains(res.Summary(), "no .env file found") {
		t.Errorf("Summary() = %q", res.Summary())
	}
}

// TestLoadWalksUpToParent covers `go run ./cmd/api` executed from a
// subdirectory: the file sits at the backend root, not the working directory.
func TestLoadWalksUpToParent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("WALKED_UP=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if err := os.Unsetenv("WALKED_UP"); err != nil {
		t.Fatal(err)
	}

	res, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if res.Path == "" {
		t.Fatal("Load() found no file; it should walk up from cmd/api to the root")
	}
	if got := os.Getenv("WALKED_UP"); got != "yes" {
		t.Errorf("WALKED_UP = %q, want yes", got)
	}
	_ = os.Unsetenv("WALKED_UP")
}

func TestSummaryNamesSkippedVariables(t *testing.T) {
	r := Result{
		Path:    "/tmp/.env",
		Applied: []string{"A", "B"},
		Skipped: []string{"GOOGLE_API_KEY"},
	}
	got := r.Summary()
	for _, want := range []string{"2 variable(s)", "/tmp/.env", "GOOGLE_API_KEY"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() = %q, want it to contain %q", got, want)
		}
	}
}
