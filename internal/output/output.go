package output

import (
	"io"
	"os"
	"path/filepath"
)

// Writer writes migrated flows to either a directory or a stream.
type Writer struct {
	dir    string
	stream io.Writer
	count  int
}

// New returns a Writer. If dir is non-empty, flows are written as individual
// files inside that directory; otherwise they are written to stream.
func New(dir string, stream io.Writer) *Writer {
	return &Writer{dir: dir, stream: stream}
}

func (w *Writer) Write(name string, content []byte) error {
	if w.dir != "" {
		return w.writeFile(name, content)
	}
	return w.writeStream(name, content)
}

func (w *Writer) writeFile(name string, content []byte) error {
	outPath := filepath.Join(w.dir, name)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, content, 0o644)
}

func (w *Writer) writeStream(_ string, content []byte) error {
	if w.count > 0 {
		if _, err := io.WriteString(w.stream, "\n"); err != nil {
			return err
		}
	}
	if _, err := w.stream.Write(content); err != nil {
		return err
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := io.WriteString(w.stream, "\n"); err != nil {
			return err
		}
	}
	w.count++
	return nil
}
