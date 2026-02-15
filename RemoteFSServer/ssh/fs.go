package ssh

import (
	"encoding/binary"
	"fmt"
	"log"
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

func (fs *sshFS) reconnect() error {
	log.Printf("SFTP connection lost, reconnecting...")

	newConn, err := sftp.NewClient(fs.client.GetConn())
	if err != nil {
		return err
	}

	fs.conn = newConn
	log.Printf("SFTP reconnected")
	return nil
}

func (fs *sshFS) ensureConnected() error {
	if fs.conn != nil {
		return nil
	}
	return fs.reconnect()
}

func (fs *sshFS) doWithReconnect(fn func(*sftp.Client) error) error {
	err := fn(fs.conn)
	if err != nil {
		log.Printf("SFTP operation failed: %v, reconnecting...", err)
		if reerr := fs.reconnect(); reerr != nil {
			return fmt.Errorf("operation failed: %v, reconnection failed: %w", err, reerr)
		}
		return fn(fs.conn)
	}
	return nil
}

func (c *sshClient) NewFS(rootDir string) (*sshFS, error) {
	conn, err := sftp.NewClient(c.conn)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(rootDir, "~/") {
		home, err := conn.Getwd()
		if err != nil {
			home = "/"
		}
		rootDir = path.Join(home, rootDir[2:])
	} else if rootDir == "" || rootDir == "~" {
		root, err := conn.Getwd()
		if err != nil {
			root = "/"
		}
		rootDir = root
	}
	return &sshFS{conn, c, nil, rootDir}, nil
}

func (fs *sshFS) Close() error {
	if fs.conn != nil {
		return fs.conn.Close()
	}
	return nil
}

func (fs *sshFS) SetCreds(creds nfsFs.Creds) {
	fs.creds = creds
}

func (fs *sshFS) Create(path string) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(path)
	handle, err := fs.conn.Create(fullPath)
	if err != nil {
		return nil, err
	}
	return &file{handle, fs.conn, false, fullPath, fs.rootDir}, nil
}

func (fs *sshFS) MkdirAll(dirPath string, mode os.FileMode) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(dirPath)
	if err := fs.conn.MkdirAll(fullPath); err != nil {
		return err
	}
	return fs.conn.Chmod(fullPath, mode)
}

func (fs *sshFS) Open(filePath string) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)
	handle, err := fs.conn.Open(fullPath)
	if err != nil {
		return nil, err
	}
	info, err := fs.conn.Lstat(fullPath)
	if err != nil {
		handle.Close()
		return nil, err
	}
	isRoot := isRootPath(filePath, fs.rootDir)
	isSymlink := info.Mode()&os.ModeSymlink != 0
	return &file{handle, fs.conn, isRoot || (info.IsDir() && !isSymlink), fullPath, fs.rootDir}, nil
}

func (fs *sshFS) OpenFile(filePath string, flag int, mode os.FileMode) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)

	if flag&os.O_CREATE != 0 {
		handle, err := fs.conn.Create(fullPath)
		if err != nil {
			return nil, err
		}
		fs.conn.Chmod(fullPath, mode)
		info, err := fs.conn.Lstat(fullPath)
		if err != nil {
			handle.Close()
			return nil, err
		}
		isRoot := isRootPath(filePath, fs.rootDir)
		isSymlink := info.Mode()&os.ModeSymlink != 0
		return &file{handle, fs.conn, isRoot || (info.IsDir() && !isSymlink), fullPath, fs.rootDir}, nil
	}

	handle, err := fs.conn.OpenFile(fullPath, flag)
	if err != nil {
		return nil, err
	}
	info, err := fs.conn.Lstat(fullPath)
	if err != nil {
		handle.Close()
		return nil, err
	}
	isRoot := isRootPath(filePath, fs.rootDir)
	isSymlink := info.Mode()&os.ModeSymlink != 0
	return &file{handle, fs.conn, isRoot || (info.IsDir() && !isSymlink), fullPath, fs.rootDir}, nil
}

func (fs *sshFS) Stat(filePath string) (nfsFs.FileInfo, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)
	info, err := fs.conn.Lstat(fullPath)
	if err != nil {
		return nil, err
	}
	return newFileInfoWithPath(info, filePath, fs.rootDir), nil
}

func (fs *sshFS) Lstat(filePath string) (nfsFs.FileInfo, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)
	info, err := fs.conn.Lstat(fullPath)
	if err != nil {
		return nil, err
	}
	return newFileInfoWithPath(info, filePath, fs.rootDir), nil
}

func (fs *sshFS) Chmod(filePath string, mode os.FileMode) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chmod(fullPath, mode)
}

func (fs *sshFS) Chown(filePath string, uid, gid int) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chown(fullPath, uid, gid)
}

func (fs *sshFS) Symlink(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullNew := fs.resolvePath(newname)
	return fs.conn.Symlink(oldname, fullNew)
}

func (fs *sshFS) Readlink(filePath string) (string, error) {
	if err := fs.ensureConnected(); err != nil {
		return "", err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.ReadLink(fullPath)
}

func (fs *sshFS) Link(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Link(oldPath, newPath)
}

func (fs *sshFS) Rename(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Rename(oldPath, newPath)
}

func (fs *sshFS) Remove(filePath string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
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
	return []byte(fs.rootDir)
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
	if p == "" || string(handle) == "/" {
		return fs.rootDir, nil
	}
	return p, nil
}

func isRootPath(p string, rootDir string) bool {
	if p == "" || p == "." || p == "/" || p == "~" {
		return true
	}
	cleanRoot := path.Clean(rootDir)
	cleanP := path.Clean(p)
	return cleanP == cleanRoot
}

func (fs *sshFS) resolvePath(p string) string {

	if isRootPath(p, fs.rootDir) {
		result := fs.rootDir
		return result
	}

	if p == "~" {
		result := fs.rootDir
		return result
	}
	if strings.HasPrefix(p, "~/") {
		result := path.Join(fs.rootDir, p[2:])
		return result
	}

	cleanRoot := path.Clean(fs.rootDir)

	if path.IsAbs(p) {
		if cleanRoot == "/" {
			result := path.Clean(p)
			return result
		}

		if strings.HasPrefix(p, cleanRoot) {
			result := path.Clean(p)
			return result
		}

		rel := p[1:]
		result := path.Join(cleanRoot, rel)
		return result
	}

	result := path.Join(cleanRoot, p)
	return result
}

func newFileInfo(info os.FileInfo) nfsFs.FileInfo {
	return &fileInfoWrapper{info: info}
}

func newFileInfoWithPath(info os.FileInfo, nfsPath string, rootDir string) nfsFs.FileInfo {
	return &fileInfoWrapper{info: info, nfsPath: nfsPath, rootDir: rootDir}
}

type fileInfoWrapper struct {
	info    os.FileInfo
	nfsPath string
	rootDir string
}

func (f *fileInfoWrapper) Name() string {
	return f.info.Name()
}

func (f *fileInfoWrapper) Size() int64 {
	return f.info.Size()
}

func (f *fileInfoWrapper) Mode() os.FileMode {
	mode := f.info.Mode()

	if mode&os.ModeSymlink != 0 {
		if isRootPath(f.nfsPath, f.rootDir) {
			if f.info.IsDir() {
				return mode | os.ModeDir
			}
		}
		return mode
	}

	if f.IsDir() {
		if mode&os.ModeDir == 0 {
			mode = mode | os.ModeDir
		}
	}

	return mode
}

func (f *fileInfoWrapper) ModTime() time.Time {
	return f.info.ModTime()
}

func (f *fileInfoWrapper) IsDir() bool {
	if isRootPath(f.nfsPath, f.rootDir) {
		return true
	}
	if f.info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return f.info.IsDir()
}

type fileStat struct {
	UID uint32
	GID uint32
}

func (f *fileInfoWrapper) Sys() any {
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
