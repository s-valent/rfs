package ssh

import (
	"path"
	"strings"

	"github.com/pkg/sftp"
	nfsFs "github.com/smallfz/libnfs-go/fs"
)

type file struct {
	handle   *sftp.File
	client   *sftp.Client
	isDir    bool
	fullPath string
	rootDir  string
}

func (f *file) Close() error {
	return f.handle.Close()
}

func (f *file) Read(p []byte) (n int, err error) {
	return f.handle.Read(p)
}

func (f *file) Write(p []byte) (n int, err error) {
	return f.handle.Write(p)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	return f.handle.Seek(offset, whence)
}

func (f *file) Name() string {
	return f.handle.Name()
}

func (f *file) Stat() (nfsFs.FileInfo, error) {
	info, err := f.handle.Stat()
	if err != nil {
		return nil, err
	}
	nfsPath := f.fullPath
	if f.rootDir != "" && strings.HasPrefix(f.fullPath, f.rootDir) {
		rel := strings.TrimPrefix(f.fullPath, f.rootDir)
		if rel == "" {
			nfsPath = "/"
		} else {
			nfsPath = rel
		}
	}
	return newFileInfoWithPath(info, nfsPath, f.rootDir), nil
}

func (f *file) Truncate() error {
	info, err := f.handle.Stat()
	if err != nil {
		return err
	}
	return f.handle.Truncate(info.Size())
}

func (f *file) Sync() error {
	return f.handle.Sync()
}

func (f *file) Readdir(n int) ([]nfsFs.FileInfo, error) {
	if !f.isDir {
		return nil, nil
	}

	dirPath := f.fullPath
	if dirPath == "" {
		dirPath = f.handle.Name()
	}

	entries, err := f.client.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	result := make([]nfsFs.FileInfo, len(entries))
	for i, entry := range entries {
		entryPath := path.Join(dirPath, entry.Name())
		result[i] = newFileInfoWithPath(entry, entryPath, f.rootDir)
	}
	return result, nil
}
