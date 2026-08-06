// Package shellquote escapes values that are interpolated into command
// strings executed by a remote shell.
//
// syncctl sends commands to the primary and secondary over SSH as a
// single string, which the remote login shell parses. Any config-derived
// value spliced into that string is shell input: a path containing a
// space silently addresses the wrong file, and one containing a
// semicolon or backtick runs as a command. Quote everything that is not
// a fixed literal.
package shellquote

import "strings"

// safeChars are the characters a POSIX shell treats identically quoted
// or bare, so a value made only of them needs no quoting.
const safeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
	"@%+=:,./-_"

// Quote returns s wrapped so a POSIX shell parses it as one literal
// word. Single quotes suppress every form of expansion; an embedded
// single quote is closed, escaped, and reopened ('\”).
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if isSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isSafe(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(safeChars, r) {
			return false
		}
	}
	return true
}
