package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/dirs"
)

// PidFile returns the path to the PID file for the given state directory.
// Lives under runtime/ in the structured layout.
func PidFile(stateDir string) string {
	return dirs.New(stateDir).PidFile()
}

// WritePID writes the current process ID to the PID file.
func WritePID(path string) error {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

// ReadPID reads the PID from the PID file.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
