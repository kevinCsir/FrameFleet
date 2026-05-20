package diskusage

import (
	"os"
	"path/filepath"
	"syscall"
)

type Usage struct {
	TotalBytes int64
	FreeBytes  int64
}

type Observer struct {
	path string
}

func NewObserver(path string) *Observer {
	return &Observer{path: path}
}

func (o *Observer) Usage() (Usage, error) {
	path := o.path
	if path == "" {
		path = "."
	}
	statPath, err := existingPath(path)
	if err != nil {
		return Usage{}, err
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(statPath, &stat); err != nil {
		return Usage{}, err
	}

	blockSize := int64(stat.Bsize)
	return Usage{
		TotalBytes: int64(stat.Blocks) * blockSize,
		FreeBytes:  int64(stat.Bavail) * blockSize,
	}, nil
}

func existingPath(path string) (string, error) {
	current := path
	for {
		if _, err := os.Stat(current); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		current = parent
	}
}
