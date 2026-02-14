package ssh

import (
	"log"

	"github.com/pkg/sftp"
	nfsFs "github.com/smallfz/libnfs-go/fs"
)

type file struct {
	handle *sftp.File
}

func (f *file) Close() error {
	log.Printf("Called Close: %v\n", f.handle.Name())
	return f.handle.Close()
}

func (f *file) Read(p []byte) (n int, err error) {
	log.Printf("Called Read: %v\n", f.handle.Name())
	return f.handle.Read(p)
}

func (f *file) Write(p []byte) (n int, err error) {
	log.Printf("Called Write: %v\n", f.handle.Name())
	return f.handle.Write(p)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	log.Printf("Called Seek: %v\n", f.handle.Name())
	return f.handle.Seek(offset, whence)
}

func (f *file) Name() string {
	log.Printf("Called Name: %v\n", f.handle.Name())
	return f.handle.Name()
}

func (f *file) Stat() (nfsFs.FileInfo, error) {
	log.Printf("Called Stat: %v\n", f.handle.Name())
	info, err := f.handle.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfo{info}, nil
}

func (f *file) Truncate() error {
	log.Printf("Called Truncate: %v\n", f.handle.Name())
	return f.handle.Truncate(0)
}

func (f *file) Sync() error {
	log.Printf("Called Sync: %v\n", f.handle.Name())
	return f.handle.Sync()
}

func (f *file) Readdir(n int) ([]nfsFs.FileInfo, error) {
	log.Printf("Called Readdir: %v\n", f.handle.Name())
	panic("not implemented")
	// return f.handle.Readdir(n)
}
