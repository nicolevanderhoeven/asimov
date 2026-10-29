//go:build !go1.21

package main

// This file is built only by a Go older than 1.21, which cannot compile the
// task runner. The identifier below is deliberately undefined: the compiler
// stops with it in its message, and the file name sorts first so that the
// message comes before the errors it explains.
var _ = Go_1_21_or_newer_is_required__run_go_version_to_check_yours
