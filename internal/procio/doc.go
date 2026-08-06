// Package procio detects when an io.Writer used for a subprocess's standard
// stream can be passed through as an inherited file descriptor instead of
// being copied to over an OS pipe, so tools like Git and gh can still detect
// a terminal and render interactive progress and color.
package procio
