// Package logfile keeps the app's log in a file. The app is a tray app started from the Dock or at
// login, so its stderr goes nowhere and an error that is only printed is lost.
package logfile

import (
	"io"
	"os"
	"path/filepath"
)

// Name is the log's file name, inside the config folder.
const Name = "deck.log"

// maxSize is how big the log may be at start before it is set aside as deck.log.1. One old file is
// kept, so the log never takes more than about twice this.
const maxSize = 2 << 20

// Open opens the log in dir for appending, setting a big one aside first. The writer sends
// everything to stderr as well, for a run from a terminal. Close the file when done.
func Open(dir string) (io.Writer, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, Name)
	if st, err := os.Stat(path); err == nil && st.Size() > maxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return io.MultiWriter(f, os.Stderr), f, nil
}
