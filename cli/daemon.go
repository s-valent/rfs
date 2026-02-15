package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rfs/ssh"

	"github.com/smallfz/libnfs-go/auth"
	"github.com/smallfz/libnfs-go/backend"
	nfsFs "github.com/smallfz/libnfs-go/fs"
	nfsLog "github.com/smallfz/libnfs-go/log"
	"github.com/smallfz/libnfs-go/server"
)

type nfsFileHandler struct {
	w io.Writer
}

func (h *nfsFileHandler) Write(msg *nfsLog.Message) {
	ts := time.Now().Format("2006/01/02 15:04:05")
	fmt.Fprintf(h.w, "%s [%s] <%s:%d> %s %s\n", ts, msg.Mod, msg.FileName, msg.LineNo, nfsLog.GetLevelName(msg.Lev), msg.Message)
}

func newNFSLogger(file *os.File) nfsLog.Logger {
	return nfsLog.NewLogger("nfs", nfsLog.INFO, &nfsFileHandler{file})
}

type mount struct {
	info      *MountInfo
	logFile   io.Closer
	sshFS     *ssh.SSHFS
	client    *ssh.SSHClient
	mu        sync.Mutex
	stopped   bool
	createdAt time.Time
}

type Daemon struct {
	socketPath string
	mounts     map[string]*mount
	mu         sync.Mutex
}

func NewDaemon() *Daemon {
	return &Daemon{
		socketPath: filepath.Join(StateDir(), "daemon.sock"),
		mounts:     make(map[string]*mount),
	}
}

const maxLogSize = 10 * 1024 * 1024 // 10MB

func (d *Daemon) openLogFile(name string) (*truncatingFile, error) {
	logPath := filepath.Join(StateDir(), "mounts", name+".log")

	os.Remove(logPath)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &truncatingFile{File: f, maxSize: maxLogSize}, nil
}
func (d *Daemon) ensureDirs() error {
	mountsDir := filepath.Join(StateDir(), "mounts")
	if err := os.MkdirAll(mountsDir, 0755); err != nil {
		return err
	}
	return nil
}

func (d *Daemon) cleanupOldLogs() {
	mountsDir := filepath.Join(StateDir(), "mounts")
	today := time.Now().Format("2006-01-02")
	entries, _ := os.ReadDir(mountsDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		path := filepath.Join(mountsDir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		modDate := info.ModTime().Format("2006-01-02")
		if modDate != today {
			os.Remove(path)
		}
	}
}

func (d *Daemon) cleanupStaleState() {
	mountsDir := filepath.Join(StateDir(), "mounts")
	entries, err := os.ReadDir(mountsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".state") {
			continue
		}
	}
}

type truncatingFile struct {
	*os.File
	maxSize int64
}

func (f *truncatingFile) Write(p []byte) (int, error) {
	if info, err := f.Stat(); err == nil && info.Size() >= f.maxSize {
		content, err := os.ReadFile(f.Name())
		if err != nil {
			return 0, err
		}

		newContent := content[len(content)/2:]
		if err := os.WriteFile(f.Name(), newContent, 0644); err != nil {
			return 0, err
		}

		f.File.Close()
		newFile, err := os.OpenFile(f.Name(), os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return 0, err
		}
		*f.File = *newFile
	}
	_, err := f.Seek(0, 2)
	if err != nil {
		return 0, err
	}
	return f.File.Write(p)
}

func (f *truncatingFile) Close() error {
	return f.File.Close()
}

func (d *Daemon) Start() error {
	if err := d.ensureDirs(); err != nil {
		return err
	}

	d.cleanupStaleState()
	d.cleanupOldLogs()

	if err := os.RemoveAll(d.socketPath); err != nil {
		return err
	}

	ln, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()

	os.Chmod(d.socketPath, 0777)

	go d.monitorMounts()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	var cmd Command
	if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
		json.NewEncoder(conn).Encode(Response{Error: err.Error()})
		return
	}

	var resp Response
	switch cmd.Type {
	case "up":
		resp = d.handleUp(cmd)
	case "ls":
		resp = d.handleList()
	case "down":
		resp = d.handleStop(cmd.Names)
	default:
		resp = Response{Error: "unknown command"}
	}

	json.NewEncoder(conn).Encode(resp)

	d.mu.Lock()
	hasMounts := len(d.mounts) > 0
	d.mu.Unlock()
	if !hasMounts {
		os.Exit(0)
	}
}

func (d *Daemon) handleUp(cmd Command) Response {
	alias := cmd.SSHAlias
	remotePath := cmd.RemotePath
	customMountDir := cmd.MountDir
	name := MountName(alias, remotePath)

	d.mu.Lock()
	_, exists := d.mounts[name]
	d.mu.Unlock()
	if exists {
		return Response{Error: "already mounted: " + name}
	}

	logFile, err := d.openLogFile(name)
	if err != nil {
		return Response{Error: "failed to create log: " + err.Error()}
	}

	m, err := d.startMount(alias, remotePath, name, customMountDir, logFile)
	if err != nil {
		logFile.Close()
		return Response{Error: err.Error()}
	}

	d.mu.Lock()
	m.createdAt = time.Now()
	d.mounts[name] = m
	d.mu.Unlock()

	d.saveState(name, m.info)

	return Response{OK: true, Mount: m.info}
}

func (d *Daemon) startMount(alias, remotePath, name, customMountDir string, logFile *truncatingFile) (*mount, error) {
	log.SetOutput(logFile)

	nfsLogger := nfsLog.NewLogger("nfs", nfsLog.INFO, &nfsFileHandler{logFile})
	nfsLog.SetLoggerDefault(nfsLogger)
	nfsLog.SetLevelName("info")

	listen, err := findFreePort()
	if err != nil {
		return nil, err
	}

	mountDir := customMountDir
	if mountDir == "" {
		mountDir = filepath.Join(StateDir(), name)
	}
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return nil, err
	}

	client, err := ssh.Connect(alias)
	if err != nil {
		os.RemoveAll(mountDir)
		return nil, fmt.Errorf("ssh connect: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		os.RemoveAll(mountDir)
		return nil, fmt.Errorf("new session: %w", err)
	}
	session.Stdout = logFile
	session.Stderr = logFile
	if err := session.Run("echo"); err != nil {
		session.Close()
		client.Close()
		os.RemoveAll(mountDir)
		return nil, fmt.Errorf("session run: %w", err)
	}
	session.Close()

	fs, err := client.NewFS(remotePath)
	if err != nil {
		client.Close()
		os.RemoveAll(mountDir)
		return nil, fmt.Errorf("new fs: %w", err)
	}

	backend := backend.New(func() nfsFs.FS { return fs }, auth.Null)
	svr, err := server.NewServerTCP(listen, backend)
	if err != nil {
		fs.Close()
		client.Close()
		os.RemoveAll(mountDir)
		return nil, fmt.Errorf("new server: %w", err)
	}

	go func() {
		if err := svr.Serve(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	time.Sleep(2 * time.Second)

	exec.Command("umount", "-f", mountDir).Run()

	port := strings.TrimPrefix(listen, ":")
	mountCmd := exec.Command("mount", "-o", fmt.Sprintf("nfsvers=4,soft,noacl,tcp,port=%s", port), "-t", "nfs", "localhost:/", mountDir)
	mountCmd.Stdout = logFile
	mountCmd.Stderr = logFile
	if err := mountCmd.Run(); err != nil {
		fmt.Fprintf(logFile, "Mount failed: %v\n", err)
	}

	m := &mount{
		info: &MountInfo{
			Name:       name,
			PID:        os.Getpid(),
			Port:       strings.TrimPrefix(listen, ":"),
			MountDir:   mountDir,
			SSHAlias:   alias,
			RemotePath: remotePath,
			StartedAt:  time.Now(),
			LogFile:    logFile.File.Name(),
		},
		logFile: logFile,
		sshFS:   fs,
		client:  client,
	}

	return m, nil
}

func (d *Daemon) handleList() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	list := make([]*MountInfo, 0, len(d.mounts))
	for _, m := range d.mounts {
		list = append(list, m.info)
	}
	return Response{OK: true, Mounts: list}
}

func (d *Daemon) handleStop(names []string) Response {
	if len(names) == 0 {
		d.mu.Lock()
		for n := range d.mounts {
			names = append(names, n)
		}
		d.mu.Unlock()
	}

	var stopped []string
	for _, name := range names {
		d.mu.Lock()
		m, ok := d.mounts[name]
		d.mu.Unlock()
		if !ok {
			continue
		}

		exec.Command("umount", "-f", m.info.MountDir).Run()

		if m.sshFS != nil {
			m.sshFS.Close()
		}
		if m.client != nil {
			m.client.Close()
		}
		if m.logFile != nil {
			m.logFile.Close()
		}

		os.RemoveAll(m.info.MountDir)

		d.mu.Lock()
		delete(d.mounts, name)
		d.mu.Unlock()
		d.deleteState(name)
		stopped = append(stopped, name)
	}

	return Response{OK: true, Names: stopped}
}

func (d *Daemon) saveState(name string, info *MountInfo) {
	data, _ := json.MarshalIndent(info, "", "  ")
	os.WriteFile(filepath.Join(StateDir(), "mounts", name+".state"), data, 0644)
}

func (d *Daemon) deleteState(name string) {
	os.Remove(filepath.Join(StateDir(), "mounts", name+".state"))
}

func (d *Daemon) cleanupDisconnected() {
	d.mu.Lock()
	var toStop []string
	for name, m := range d.mounts {
		if time.Since(m.createdAt) < 10*time.Second {
			continue
		}
		connected := m.client == nil || m.client.IsConnected()
		mounted := isMounted(m.info.MountDir)
		if !connected {
			toStop = append(toStop, name)
			log.Printf("cleanup: %s disconnected", name)
			continue
		}
		if !mounted {
			toStop = append(toStop, name)
			log.Printf("cleanup: %s not mounted (path=%s)", name, m.info.MountDir)
		}
	}
	d.mu.Unlock()

	for _, name := range toStop {
		d.handleStop([]string{name})
	}
}

func isMounted(path string) bool {
	out, err := exec.Command("mount").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), " on "+path+" ")
}

func (d *Daemon) monitorMounts() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		d.cleanupDisconnected()
	}
}

func findFreePort() (string, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	addr := l.Addr().(*net.TCPAddr)
	return fmt.Sprintf(":%d", addr.Port), nil
}
