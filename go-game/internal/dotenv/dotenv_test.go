package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSetsOnlyMissingVariables(t *testing.T) {
	t.Setenv("DOTENV_TEST_KEPT", "from the shell")
	os.Unsetenv("DOTENV_TEST_NEW")
	t.Cleanup(func() { os.Unsetenv("DOTENV_TEST_NEW") })
	path := write(t, "DOTENV_TEST_KEPT=from the file\nDOTENV_TEST_NEW=from the file\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DOTENV_TEST_KEPT"); got != "from the shell" {
		t.Errorf("shell value replaced: %q", got)
	}
	if got := os.Getenv("DOTENV_TEST_NEW"); got != "from the file" {
		t.Errorf("file value not set: %q", got)
	}
}

func TestLoadAcceptsByteOrderMarkAndWindowsLineEndings(t *testing.T) {
	os.Unsetenv("DOTENV_TEST_BOM")
	os.Unsetenv("DOTENV_TEST_CRLF")
	t.Cleanup(func() { os.Unsetenv("DOTENV_TEST_BOM"); os.Unsetenv("DOTENV_TEST_CRLF") })
	path := write(t, "\xef\xbb\xbfDOTENV_TEST_BOM=one\r\nDOTENV_TEST_CRLF=two\r\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DOTENV_TEST_BOM"); got != "one" {
		t.Errorf("first variable = %q, want one", got)
	}
	if got := os.Getenv("DOTENV_TEST_CRLF"); got != "two" {
		t.Errorf("second variable = %q, want two", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope")); !os.IsNotExist(err) {
		t.Errorf("err = %v, want a not-exist error", err)
	}
}
