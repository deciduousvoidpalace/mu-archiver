// Package util holds small helpers shared across mu-dl: filename sanitizing,
// human-readable byte sizes, and terminal input.
package util

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

var (
	// characters that are illegal or troublesome in filenames on common
	// filesystems (Linux is permissive, but we stay portable).
	badChars   = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)
	multiSpace = regexp.MustCompile(`\s+`)
)

// SanitizeFilename turns an arbitrary title into a safe file/directory name.
// It never returns an empty string; "untitled" is used as a fallback.
func SanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = badChars.ReplaceAllString(s, " ")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .")
	// keep names from getting absurdly long
	if len(s) > 180 {
		s = strings.TrimSpace(s[:180])
	}
	if s == "" {
		return "untitled"
	}
	return s
}

// ExtFromURL returns the file extension (including the dot) of a URL path,
// defaulting to fallback when none is present.
func ExtFromURL(rawurl, fallback string) string {
	// strip query/fragment before looking at the path
	if i := strings.IndexAny(rawurl, "?#"); i >= 0 {
		rawurl = rawurl[:i]
	}
	ext := path.Ext(rawurl)
	if ext == "" || len(ext) > 6 {
		return fallback
	}
	return ext
}

// HumanBytes formats a byte count like "12.3 MiB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Prompt reads a single line from stdin after printing label.
func Prompt(label string) (string, error) {
	fmt.Print(label)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// PromptPassword reads a line without echoing it. On Linux it toggles the
// terminal echo flag via stty; if that is unavailable it falls back to a
// visible read so the tool still works in constrained environments.
func PromptPassword(label string) (string, error) {
	fmt.Print(label)
	// try to disable echo
	disable := exec.Command("stty", "-echo")
	disable.Stdin = os.Stdin
	echoDisabled := disable.Run() == nil
	if echoDisabled {
		defer func() {
			restore := exec.Command("stty", "echo")
			restore.Stdin = os.Stdin
			_ = restore.Run()
			fmt.Println()
		}()
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
