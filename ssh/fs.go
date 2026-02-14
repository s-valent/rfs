package ssh

import (
	"log"
	"os"
	"time"

	"github.com/pkg/sftp"
	nfsFs "github.com/smallfz/libnfs-go/fs"
)

type sshFS struct {
	conn *sftp.Client
}

func (c *sshClient) NewFS() (*sshFS, error) {
	conn, err := sftp.NewClient(c.conn)
	if err != nil {
		return nil, err
	}
	return &sshFS{conn}, nil
}

func (fs *sshFS) Close() error {
	return fs.conn.Close()
}

func (fs *sshFS) Create(path string) (nfsFs.File, error) {
	log.Printf("Called Create: %v\n", path)
	handle, err := fs.conn.Create(path)
	if err != nil {
		return nil, err
	}
	return &file{handle}, nil
}

func (fs *sshFS) MkdirAll(path string, mode os.FileMode) error {
	log.Printf("Called MkdirAll: %v\n", path)
	return fs.conn.MkdirAll(path)
}

func (fs *sshFS) Open(path string) (nfsFs.File, error) {
	log.Printf("Called Open: %v\n", path)
	handle, err := fs.conn.Open(path)
	if err != nil {
		return nil, err
	}
	return &file{handle}, nil
}

func (fs *sshFS) OpenFile(path string, flag int, mode os.FileMode) (nfsFs.File, error) {
	log.Printf("Called OpenFile: %v\n", path)
	handle, err := fs.conn.OpenFile(path, flag)
	if err != nil {
		return nil, err
	}
	return &file{handle}, nil
}

func (fs *sshFS) Attributes() *nfsFs.Attributes {
	log.Printf("Called Attributes\n")
	return &nfsFs.Attributes{
		LinkSupport:     true,
		SymlinkSupport:  true,
		ChownRestricted: true,
		MaxName:         255,
		MaxRead:         1024 * 1024 * 1024,
		MaxWrite:        1024 * 1024 * 1024,
		NoTrunc:         true,
	}
}

func (fs *sshFS) Chmod(path string, mode os.FileMode) error {
	log.Printf("Called Chmod: %v\n", path)
	return fs.conn.Chmod(path, mode)
}

func (fs *sshFS) Chown(path string, uid, gid int) error {
	log.Printf("Called Chown: %v\n", path)
	return fs.conn.Chown(path, uid, gid)
}

func (fs *sshFS) Symlink(oldname, newname string) error {
	log.Printf("Called Symlink: %v\n", oldname)
	return fs.conn.Symlink(oldname, newname)
}

func (fs *sshFS) Readlink(path string) (string, error) {
	log.Printf("Called Readlink: %v\n", path)
	return fs.conn.ReadLink(path)
}

func (fs *sshFS) GetFileId(info nfsFs.FileInfo) uint64 {
	log.Printf("Called GetFileId: %v\n", info.Name())
	return uint64(info.Sys().(uint64))
}

func (fs *sshFS) GetRootHandle() []byte {
	log.Printf("Called GetRootHandle\n")
	return []byte{}
}

func (fs *sshFS) GetHandle(info nfsFs.FileInfo) ([]byte, error) {
	log.Printf("Called GetHandle: %v\n", info.Name())
	return []byte{}, nil
}

func (fs *sshFS) ResolveHandle(handle []byte) (string, error) {
	log.Printf("Called ResolveHandle: %v\n", handle)
	return "", nil
}

func (fs *sshFS) getLink(path string) (string, error) {
	log.Printf("Called getLink: %v\n", path)
	return fs.conn.ReadLink(path)
}

func (fs *sshFS) Link(oldname, newname string) error {
	log.Printf("Called Link: %v\n", oldname)
	return fs.conn.Link(oldname, newname)
}

func (fs *sshFS) SetCreds(nfsFs.Creds) {
	log.Printf("Called SetCreds\n")
}

type fileInfo struct {
	os.FileInfo
}

func (f fileInfo) ATime() time.Time { log.Printf("Called ATime: %v\n", f.Name()); return time.Time{} }
func (f fileInfo) CTime() time.Time { log.Printf("Called CTime: %v\n", f.Name()); return time.Time{} }
func (f fileInfo) NumLinks() int    { log.Printf("Called NumLinks: %v\n", f.Name()); return 0 }

func (fs *sshFS) Stat(path string) (nfsFs.FileInfo, error) {
	log.Printf("Called Stat: %v\n", path)
	info, err := fs.conn.Stat(path)
	if err != nil {
		return nil, err
	}
	return fileInfo{info}, nil
}

func (fs *sshFS) Rename(oldname, newname string) error {
	log.Printf("Called Rename: %v\n", oldname)
	return fs.conn.Rename(oldname, newname)
}

func (fs *sshFS) Remove(path string) error {
	log.Printf("Called Remove: %v\n", path)
	return fs.conn.Remove(path)
}
