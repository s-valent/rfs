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
	"sync"
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

type dirCacheEntry struct {
	entries []os.FileInfo
	expiry  time.Time
}

type SSHFS struct {
	conn       *sftp.Client
	client     *SSHClient
	creds      nfsFs.Creds
	rootDir    string
	dirCache   map[string]dirCacheEntry
	dirCacheMu sync.Mutex
}

func (fs *SSHFS) reconnect() error {
	log.Printf("SFTP connection lost, reconnecting...")

	if err := fs.client.EnsureConnected(); err != nil {
		return fmt.Errorf("ssh reconnect failed: %w", err)
	}

	newConn, err := sftp.NewClient(fs.client.GetConn())
	if err != nil {
		return err
	}

	fs.conn = newConn
	fs.clearDirCache()
	log.Printf("SFTP reconnected")
	return nil
}

func (fs *SSHFS) ensureConnected() error {
	if fs.conn == nil {
		return fs.reconnect()
	}
	_, err := fs.conn.Lstat(".")
	if err != nil {
		log.Printf("SFTP connection stale, reconnecting...")
		return fs.reconnect()
	}
	return nil
}

func (fs *SSHFS) doWithReconnect(fn func(*sftp.Client) error) error {
	err := fn(fs.conn)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "file does not exist") || strings.Contains(errStr, "no such file") {
			return err
		}
		log.Printf("SFTP operation failed: %v, reconnecting...", err)
		if reerr := fs.reconnect(); reerr != nil {
			return fmt.Errorf("operation failed: %v, reconnection failed: %w", err, reerr)
		}
		return fn(fs.conn)
	}
	return nil
}

func (c *SSHClient) NewFS(rootDir string) (*SSHFS, error) {
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
	return &SSHFS{conn, c, nil, rootDir, make(map[string]dirCacheEntry), sync.Mutex{}}, nil
}

func (fs *SSHFS) Close() error {
	if fs.conn != nil {
		return fs.conn.Close()
	}
	return nil
}

func (fs *SSHFS) getDirCache(dirPath string) ([]os.FileInfo, bool) {
	fs.dirCacheMu.Lock()
	defer fs.dirCacheMu.Unlock()
	entry, ok := fs.dirCache[dirPath]
	if ok && time.Now().Before(entry.expiry) {
		return entry.entries, true
	}
	return nil, false
}

func (fs *SSHFS) setDirCache(dirPath string, entries []os.FileInfo) {
	fs.dirCacheMu.Lock()
	defer fs.dirCacheMu.Unlock()
	fs.dirCache[dirPath] = dirCacheEntry{
		entries: entries,
		expiry:  time.Now().Add(5 * time.Second),
	}
}

func (fs *SSHFS) clearDirCache() {
	fs.dirCacheMu.Lock()
	defer fs.dirCacheMu.Unlock()
	fs.dirCache = make(map[string]dirCacheEntry)
}

func (fs *SSHFS) getParentDir(filePath string) (string, string, bool) {
	dirPath := path.Dir(filePath)
	isRoot := isRootPath(filePath, fs.rootDir)
	if isRoot {
		dirPath = path.Dir(fs.rootDir)
	}
	fullDirPath := fs.resolvePath(dirPath)
	if isRoot {
		fullDirPath = path.Dir(fs.rootDir)
	}
	return dirPath, fullDirPath, isRoot
}

func (fs *SSHFS) findInCache(filePath string, dirPath string) (nfsFs.FileInfo, bool) {
	if filePath == "/" || filePath == "" {
		return nil, false
	}
	entries, ok := fs.getDirCache(dirPath)
	if !ok {
		return nil, false
	}
	baseName := path.Base(filePath)
	for _, e := range entries {
		if e.Name() == baseName {
			return newFileInfoWithPath(e, filePath, fs.rootDir), true
		}
	}
	return nil, true // cache exists but file not found
}

func (fs *SSHFS) populateDirCache(dirPath, fullDirPath string) {
	if entries, err := fs.conn.ReadDir(fullDirPath); err == nil {
		fs.setDirCache(dirPath, entries)
	}
}

func (fs *SSHFS) SetCreds(creds nfsFs.Creds) {
	fs.creds = creds
}

func (fs *SSHFS) Create(path string) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(path)
	handle, err := fs.conn.Create(fullPath)
	if err != nil {
		return nil, err
	}
	return &file{handle, fs.conn, fs, false, fullPath, fs.rootDir}, nil
}

func (fs *SSHFS) MkdirAll(dirPath string, mode os.FileMode) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(dirPath)
	if err := fs.conn.MkdirAll(fullPath); err != nil {
		return err
	}
	fs.populateDirCache(path.Dir(dirPath), path.Dir(fullPath))
	return nil
}

func (fs *SSHFS) Open(filePath string) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)
	var result nfsFs.File
	err := fs.doWithReconnect(func(conn *sftp.Client) error {
		handle, err := conn.Open(fullPath)
		if err != nil {
			return err
		}
		info, err := conn.Lstat(fullPath)
		if err != nil {
			handle.Close()
			return err
		}
		f, err := fs.newFile(handle, filePath, fullPath, info)
		if err != nil {
			return err
		}
		result = f
		return nil
	})
	return result, err
}

func (fs *SSHFS) OpenFile(filePath string, flag int, mode os.FileMode) (nfsFs.File, error) {
	if err := fs.ensureConnected(); err != nil {
		return nil, err
	}
	fullPath := fs.resolvePath(filePath)

	var result nfsFs.File
	err := fs.doWithReconnect(func(conn *sftp.Client) error {
		var handle *sftp.File
		var err error

		if flag&os.O_CREATE != 0 {
			handle, err = conn.Create(fullPath)
			if err != nil {
				return err
			}
			conn.Chmod(fullPath, mode)
		} else {
			handle, err = conn.OpenFile(fullPath, flag)
			if err != nil {
				return err
			}
		}

		info, err := conn.Lstat(fullPath)
		if err != nil {
			handle.Close()
			return err
		}
		f, err := fs.newFile(handle, filePath, fullPath, info)
		if err != nil {
			return err
		}
		result = f
		return nil
	})
	return result, err
}

func (fs *SSHFS) newFile(handle *sftp.File, filePath, fullPath string, info os.FileInfo) (nfsFs.File, error) {
	isRoot := isRootPath(filePath, fs.rootDir)
	isSymlink := info.Mode()&os.ModeSymlink != 0
	return &file{handle, fs.conn, fs, isRoot || (info.IsDir() && !isSymlink), fullPath, fs.rootDir}, nil
}

func (fs *SSHFS) Stat(filePath string) (nfsFs.FileInfo, error) {
	dirPath, fullDirPath, _ := fs.getParentDir(filePath)

	if info, inCache := fs.findInCache(filePath, dirPath); inCache {
		if info != nil {
			return info, nil
		}
		return nil, os.ErrNotExist
	}

	fullPath := fs.resolvePath(filePath)
	var result nfsFs.FileInfo
	err := fs.doWithReconnect(func(conn *sftp.Client) error {
		info, err := conn.Lstat(fullPath)
		if err != nil {
			fs.populateDirCache(dirPath, fullDirPath)
			return err
		}
		fs.populateDirCache(dirPath, fullDirPath)
		result = newFileInfoWithPath(info, filePath, fs.rootDir)
		return nil
	})
	return result, err
}

func (fs *SSHFS) Lstat(filePath string) (nfsFs.FileInfo, error) {
	dirPath, fullDirPath, _ := fs.getParentDir(filePath)

	if info, inCache := fs.findInCache(filePath, dirPath); inCache {
		if info != nil {
			return info, nil
		}
		return nil, os.ErrNotExist
	}

	fullPath := fs.resolvePath(filePath)
	var result nfsFs.FileInfo
	err := fs.doWithReconnect(func(conn *sftp.Client) error {
		info, err := conn.Lstat(fullPath)
		if err != nil {
			fs.populateDirCache(dirPath, fullDirPath)
			return err
		}
		fs.populateDirCache(dirPath, fullDirPath)
		result = newFileInfoWithPath(info, filePath, fs.rootDir)
		return nil
	})
	return result, err
}

func (fs *SSHFS) Chmod(filePath string, mode os.FileMode) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chmod(fullPath, mode)
}

func (fs *SSHFS) Chown(filePath string, uid, gid int) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Chown(fullPath, uid, gid)
}

func (fs *SSHFS) Symlink(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullNew := fs.resolvePath(newname)
	return fs.conn.Symlink(oldname, fullNew)
}

func (fs *SSHFS) Readlink(filePath string) (string, error) {
	if err := fs.ensureConnected(); err != nil {
		return "", err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.ReadLink(fullPath)
}

func (fs *SSHFS) Link(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Link(oldPath, newPath)
}

func (fs *SSHFS) Rename(oldname, newname string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	oldPath := fs.resolvePath(oldname)
	newPath := fs.resolvePath(newname)
	return fs.conn.Rename(oldPath, newPath)
}

func (fs *SSHFS) Remove(filePath string) error {
	if err := fs.ensureConnected(); err != nil {
		return err
	}
	fullPath := fs.resolvePath(filePath)
	return fs.conn.Remove(fullPath)
}

func (fs *SSHFS) Attributes() *nfsFs.Attributes {
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

func (fs *SSHFS) GetFileId(info nfsFs.FileInfo) uint64 {
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

func (fs *SSHFS) GetRootHandle() []byte {
	h := encodePath(fs.rootDir)
	return h
}

func (fs *SSHFS) GetHandle(info nfsFs.FileInfo) ([]byte, error) {
	nfsPath := getNFSPath(info)
	if nfsPath != "" {
		return encodePath(nfsPath), nil
	}
	return encodePath(info.Name()), nil
}

func (fs *SSHFS) ResolveHandle(handle []byte) (string, error) {
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

func (fs *SSHFS) resolvePath(p string) string {

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
