package ssh

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	nfsFs "github.com/smallfz/libnfs-go/fs"
)

var currentUID uint32
var currentGID uint32

func init() {
	if u, err := user.Current(); err == nil {
		if uid, err := strconv.ParseUint(u.Uid, 10, 32); err == nil {
			currentUID = uint32(uid)
		}
		if gid, err := strconv.ParseUint(u.Gid, 10, 32); err == nil {
			currentGID = uint32(gid)
		}
	} else {
		currentUID = 501
		currentGID = 20
	}
}

type sshFS struct {
	conn    *sftp.Client
	client  *sshClient
	creds   nfsFs.Creds
	rootDir string
}

func (c *sshClient) NewFS(rootDir string) (*sshFS, error) {
	conn, err := sftp.NewClient(c.conn)
	if err != nil {
		return nil, err
	}
	if rootDir == "" {
		rootDir = "/"
	}
	return &sshFS{conn, c, nil, rootDir}, nil
}

func (fs *sshFS) Close() error {
	return fs.conn.Close()
}

func (fs *sshFS) SetCreds(creds nfsFs.Creds) {
	fs.creds = creds
}

func (fs *sshFS) Create(path string) (nfsFs.File, error) {
	fullPath := fs.resolvePath(path)
	handle, err := fs.conn.Create(fullPath)
	if err != nil {
		return nil, err
	}
	return &file{handle, fs.conn, false, fullPath}, nil
}

func (fs *sshFS) MkdirAll(dirPath string, mode os.FileMode) error {
	fullPath := fs.resolvePath(dirPath)
	if err := fs.conn.MkdirAll(fullPath); err != nil {
		return err
	}
	return fs.conn.Chmod(fullPath, mode)
}

func (fs *sshFS) Open(filePath string) (nfsFs.File, error) {
	fullPath := fs.resolvePath(filePath)
	handle, err := fs.conn.Open(fullPath)
	if err != nil {
		return nil, err
	}
	info, err := handle.Stat()
	if err != nil {
		handle.Close()
		return &file{handle, fs.conn, true, fullPath}, nil
	}
	return &file{handle, fs.conn, info.IsDir(), fullPath}, nil
}

func (fs *sshFS) OpenFile(filePath string, flag int, mode os.FileMode) (nfsFs.File, error) {
	fullPath := fs.resolvePath(filePath)

	if flag&os.O_CREATE != 0 {
		handle, err := fs.conn.Create(fullPath)
		if err != nil {
			return nil, err
		}
		fs.conn.Chmod(fullPath, mode)
		info, err := handle.Stat()
		if err != nil {
			handle.Close()
			return &file{handle, fs.conn, true, fullPath}, nil
		}
		return &file{handle, fs.conn, info.IsDir(), fullPath}, nil
	}

	handle, err := fs.conn.OpenFile(fullPath, flag)
	if err != nil {
		return nil, err
	}
	info, err := handle.Stat()
	if err != nil {
		handle.Close()
		return &file{handle, fs.conn, true, fullPath}, nil
	}
	return &file{handle, fs.conn, info.IsDir(), fullPath}, nil
}

func (fs *sshFS) Stat(filePath string) (nfsFs.FileInfo, error) {
	fullPath := fs.resolvePath(filePath)
	info, err := fs.conn.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	return newFileInfoWithPath(info, filePath), nil
}

func (fs *sshFS) Lstat(filePath string) (nfsFs.FileInfo, error) {
	fullPath := fs.resolvePath(filePath)
	info, err := fs.conn.Lstat(fullPath)
	if err != nil {
		return nil, err
	}
	return newFileInfoWithPath(info, filePath), nil
}

func (fs *sshFS) Chmod(filePath string, mode os.FileMode) error {
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chmod(fullPath, mode)
}

func (fs *sshFS) Chown(filePath string, uid, gid int) error {
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chown(fullPath, uid, gid)
}

func (fs *sshFS) Symlink(oldname, newname string) error {
	fullNew := fs.resolvePath(newname)
	return fs.conn.Symlink(oldname, fullNew)
}

func (fs *sshFS) Readlink(filePath string) (string, error) {
	fullPath := fs.resolvePath(filePath)
	return fs.conn.ReadLink(fullPath)
}

func (fs *sshFS) Link(oldname, newname string) error {
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Link(oldPath, newPath)
}

func (fs *sshFS) Rename(oldname, newname string) error {
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Rename(oldPath, newPath)
}

func (fs *sshFS) Remove(filePath string) error {
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Remove(fullPath)
}

func (fs *sshFS) Attributes() *nfsFs.Attributes {
	return &nfsFs.Attributes{
		LinkSupport:     true,
		SymlinkSupport:  true,
		ChownRestricted: false,
		MaxName:         255,
		MaxRead:         1024 * 1024 * 1024,
		MaxWrite:        1024 * 1024 * 1024,
		NoTrunc:         true,
	}
}

func (fs *sshFS) GetFileId(info nfsFs.FileInfo) uint64 {
	sys := info.Sys()
	if sys == nil {
		return hashPath(info.Name())
	}

	if st, ok := sys.(*fileStat); ok {
		key := fmt.Sprintf("%s:%d:%d:%d", info.Name(), st.UID, st.GID, info.Size())
		return hashString(key)
	}

	return hashPath(info.Name())
}

func (fs *sshFS) GetRootHandle() []byte {
	return []byte("/")
}

func (fs *sshFS) GetHandle(info nfsFs.FileInfo) ([]byte, error) {
	nfsPath := getNFSPath(info)
	if nfsPath != "" {
		return encodePath(nfsPath), nil
	}
	return encodePath(info.Name()), nil
}

func (fs *sshFS) ResolveHandle(handle []byte) (string, error) {
	p := decodePath(handle)
	if p == "" {
		return string(handle), nil
	}
	return p, nil
}

func (fs *sshFS) resolvePath(p string) string {
	if p == "" || p == "." {
		return fs.rootDir
	}

	cleanRoot := path.Clean(fs.rootDir)

	if path.IsAbs(p) {
		if cleanRoot == "/" {
			return path.Clean(p)
		}

		if strings.HasPrefix(p, cleanRoot) {
			return path.Clean(p)
		}

		rel := p[1:]
		return path.Join(cleanRoot, rel)
	}

	return path.Join(cleanRoot, p)
}

func newFileInfo(info os.FileInfo) nfsFs.FileInfo {
	return &fileInfoWrapper{info: info}
}

func newFileInfoWithPath(info os.FileInfo, nfsPath string) nfsFs.FileInfo {
	return &fileInfoWrapper{info: info, nfsPath: nfsPath}
}

type fileInfoWrapper struct {
	info    os.FileInfo
	nfsPath string
}

func (f *fileInfoWrapper) Name() string {
	return f.info.Name()
}

func (f *fileInfoWrapper) Size() int64 {
	return f.info.Size()
}

func (f *fileInfoWrapper) Mode() os.FileMode {
	mode := f.info.Mode()

	if f.info.IsDir() {
		if mode&os.ModeDir == 0 {
			mode = mode | os.ModeDir
		}
		return mode
	}

	return mode
}

func (f *fileInfoWrapper) ModTime() time.Time {
	return f.info.ModTime()
}

func (f *fileInfoWrapper) IsDir() bool {
	return f.info.IsDir()
}

type fileStat struct {
	UID uint32
	GID uint32
}

func (f *fileInfoWrapper) Sys() interface{} {
	return &fileStat{UID: currentUID, GID: currentGID}
}

func (f *fileInfoWrapper) ATime() time.Time {
	sys := f.info.Sys()
	if sys == nil {
		return f.info.ModTime()
	}

	if st, ok := sys.(*sftp.FileStat); ok {
		return time.Unix(int64(st.Atime), 0)
	}

	return f.info.ModTime()
}

func (f *fileInfoWrapper) CTime() time.Time {
	return f.info.ModTime()
}

func (f *fileInfoWrapper) NumLinks() int {
	if f.info.IsDir() {
		return 2
	}
	return 1
}

func encodePath(p string) []byte {
	buf := make([]byte, 4+len(p))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(p)))
	copy(buf[4:], p)
	return buf
}

func decodePath(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(b[0:4])
	if n > uint32(len(b)-4) {
		return ""
	}
	return string(b[4 : 4+n])
}

func hashPath(p string) uint64 {
	return hashString(p)
}

func hashString(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func getNFSPath(info nfsFs.FileInfo) string {
	if w, ok := info.(*fileInfoWrapper); ok {
		return w.nfsPath
	}
	return ""
}
