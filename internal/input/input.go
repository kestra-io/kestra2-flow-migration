package input

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Flow holds the name and raw YAML content of a single Kestra flow file.
type Flow struct {
	Name    string
	Content []byte
}

// Resolve accepts a list of file paths and/or directories and returns all
// YAML flows found. Directories are walked recursively.
func Resolve(paths []string) ([]Flow, error) {
	var flows []Flow
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			found, err := resolveDir(p)
			if err != nil {
				return nil, err
			}
			flows = append(flows, found...)
		} else {
			f, err := resolveFile(p)
			if err != nil {
				return nil, err
			}
			flows = append(flows, f)
		}
	}
	return flows, nil
}

func resolveDir(dir string) ([]Flow, error) {
	var flows []Flow
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yml" && ext != ".yaml" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = filepath.Base(path)
		}
		flows = append(flows, Flow{Name: rel, Content: content})
		return nil
	})
	return flows, err
}

func resolveFile(path string) (Flow, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Flow{}, err
	}
	return Flow{
		Name:    filepath.Base(path),
		Content: content,
	}, nil
}
