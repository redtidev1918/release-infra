package static

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	rgerrors "github.com/redtidev1918/releasegraph/internal/errors"
)

var dangerousPatterns = []string{
	"--cleanup-tag",
	"git push --delete origin",
	"git push origin --delete",
}

func Check(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !isChecked(entry.Name()) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return CheckText(path, string(data))
	})
}

func CheckText(path, text string) error {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	for _, pattern := range dangerousPatterns {
		if strings.Contains(normalized, pattern) {
			return rgerrors.New(rgerrors.InvariantViolation, "dangerous tag mutation in "+path+": "+pattern)
		}
	}
	for _, line := range strings.Split(normalized, "\n") {
		if strings.Contains(line, "git push") && strings.Contains(line, "--force") && strings.Contains(line, "tag") && !strings.Contains(line, "refs/tags/v1 ") {
			return rgerrors.New(rgerrors.InvariantViolation, "forced historical tag push in "+path)
		}
	}
	return nil
}

func isChecked(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".sh") || strings.HasSuffix(name, ".bash")
}
