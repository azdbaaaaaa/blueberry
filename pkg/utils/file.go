package utils

import (
	"os"
	"path/filepath"
)

func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func GetFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func JoinPath(elem ...string) string {
	return filepath.Join(elem...)
}

