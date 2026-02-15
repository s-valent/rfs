package main

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

	ssh "remote-fs/ssh"

	"github.com/smallfz/libnfs-go/auth"
	"github.com/smallfz/libnfs-go/backend"
	nfsFs "github.com/smallfz/libnfs-go/fs"
	"github.com/smallfz/libnfs-go/server"
)

var stateDir string

func init() {
	home, _ := os.UserHomeDir()
	stateDir = filepath.Join(home, ".remote-fs")
}

type Command struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	SSHAlias   string `json:"sshAlias"`
	RemotePath string `json:"remotePath"`
	MountDir   string `json:"mountDir,omitempty"`
}

type Response struct {
	OK     bool         `json:"ok"`
	Error  string       `json:"error,omitempty"`
	Mount  *MountInfo   `json:"mount,omitempty"`
	Mounts []*MountInfo `json:"mounts,omitempty"`
}

type MountInfo struct {
	Name       string    `json:"name"`
	PID        int       `json:"pid"`
	Port       string    `json:"port"`
	MountDir   string    `json:"mountDir"`
	SSHAlias   string    `json:"sshAlias"`
	RemotePath string    `json:"remotePath"`
	StartedAt  time.Time `json:"startedAt"`
	LogFile    string    `json:"logFile"`
}

type mount struct {
	info    *MountInfo
	logFile *os.File
	sshFS   *ssh.SSHFS
	client  *ssh.SSHClient
	mu      sync.Mutex
	stopped bool
}

type Daemon struct {
	socketPath string
	mounts     map[string]*mount
	mu         sync.Mutex
}

func NewDaemon() *Daemon {
	return &Daemon{
		socketPath: filepath.Join(stateDir, "daemon.sock"),
		mounts:     make(map[string]*mount),
	}
}

func (d *Daemon) ensureDirs() error {
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}
	mountsDir := filepath.Join(stateDir, "mounts")
	if err := os.MkdirAll(mountsDir, 0755); err != nil {
		return err
	}
	return nil
}

func (d *Daemon) cleanupStaleState() {
	mountsDir := filepath.Join(stateDir, "mounts")
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

func (d *Daemon) Start() error {
	if err := d.ensureDirs(); err != nil {
		return err
	}

	d.cleanupStaleState()

	if err := os.RemoveAll(d.socketPath); err != nil {
		return err
	}

	ln, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()

	os.Chmod(d.socketPath, 0777)

	log.Println("Daemon started, socket:", d.socketPath)

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
	case "start":
		resp = d.handleStart(cmd)
	case "list":
		resp = d.handleList()
	case "stop":
		resp = d.handleStop(cmd.Name)
	default:
		resp = Response{Error: "unknown command"}
	}

	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleStart(cmd Command) Response {
	alias := cmd.SSHAlias
	remotePath := cmd.RemotePath
	customMountDir := cmd.MountDir
	name := mountName(alias, remotePath)

	d.mu.Lock()
	_, exists := d.mounts[name]
	d.mu.Unlock()
	if exists {
		return Response{Error: "already mounted: " + name}
	}

	logFile, err := os.OpenFile(
		filepath.Join(stateDir, "mounts", name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return Response{Error: "failed to create log: " + err.Error()}
	}

	m, err := d.startMount(alias, remotePath, name, customMountDir, logFile)
	if err != nil {
		logFile.Close()
		return Response{Error: err.Error()}
	}

	d.mu.Lock()
	d.mounts[name] = m
	d.mu.Unlock()

	d.saveState(name, m.info)

	return Response{OK: true, Mount: m.info}
}

func (d *Daemon) startMount(alias, remotePath, name, customMountDir string, logFile *os.File) (*mount, error) {
	listen, err := findFreePort()
	if err != nil {
		return nil, err
	}

	mountDir := customMountDir
	if mountDir == "" {
		mountDir = filepath.Join(stateDir, name)
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
		logFile.WriteString(fmt.Sprintf("Mount failed: %v\n", err))
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
			LogFile:    logFile.Name(),
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

func (d *Daemon) handleStop(name string) Response {
	d.mu.Lock()
	m, ok := d.mounts[name]
	d.mu.Unlock()
	if !ok {
		return Response{Error: "not found: " + name}
	}

	// Unmount
	exec.Command("umount", "-f", m.info.MountDir).Run()

	// Close SSH
	if m.sshFS != nil {
		m.sshFS.Close()
	}
	if m.client != nil {
		m.client.Close()
	}
	if m.logFile != nil {
		m.logFile.Close()
	}

	// Remove mount dir
	os.RemoveAll(m.info.MountDir)

	d.mu.Lock()
	delete(d.mounts, name)
	d.mu.Unlock()
	d.deleteState(name)

	return Response{OK: true}
}

func (d *Daemon) saveState(name string, info *MountInfo) {
	data, _ := json.MarshalIndent(info, "", "  ")
	os.WriteFile(filepath.Join(stateDir, "mounts", name+".state"), data, 0644)
}

func (d *Daemon) deleteState(name string) {
	os.Remove(filepath.Join(stateDir, "mounts", name+".state"))
}

func mountName(alias, path string) string {
	if path == "" || path == "/" {
		return alias
	}
	if path == "~" {
		return alias + ":~"
	}
	safePath := strings.TrimPrefix(path, "/")
	safePath = strings.ReplaceAll(safePath, "/", ":")
	return alias + ":" + safePath
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

// CLI functions
func connect() net.Conn {
	socketPath := filepath.Join(stateDir, "daemon.sock")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Println("Daemon not running, starting...")
		startDaemon()
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			conn, err = net.Dial("unix", socketPath)
			if err == nil {
				return conn
			}
		}
		return nil
	}
	return conn
}

func startDaemon() {
	execPath, _ := os.Executable()
	cmd := exec.Command(execPath, "daemon")
	cmd.Stderr = os.Stderr
	cmd.Start()
}

func sendCmd(cmd Command) *Response {
	conn := connect()
	if conn == nil {
		return &Response{Error: "daemon not running"}
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return &Response{Error: err.Error()}
	}

	var resp Response
	json.NewDecoder(conn).Decode(&resp)
	return &resp
}

func runCLI() {
	args := os.Args[1:]
	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "start":
		if len(args) < 1 {
			fmt.Println("Usage: rfs start <alias>[:<path>] [mountpoint]")
			os.Exit(1)
		}
		alias, path := parseTarget(args[0])
		mountDir := ""
		if len(args) >= 2 {
			mountDir = args[1]
		}
		resp := sendCmd(Command{Type: "mount", SSHAlias: alias, RemotePath: path, MountDir: mountDir})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		_ = mountDir // TODO: pass to daemon
		fmt.Println("Mounted:", resp.Mount.Name)
		fmt.Println("  Mount point:", resp.Mount.MountDir)
		fmt.Println("  NFS:", "localhost:"+resp.Mount.Port)

	case "list":
		resp := sendCmd(Command{Type: "list"})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		if len(resp.Mounts) == 0 {
			fmt.Println("No mounts")
			return
		}
		for _, m := range resp.Mounts {
			fmt.Printf("%s:%s  %s  port:%s  dir:%s\n", m.SSHAlias, m.RemotePath, m.Name, m.Port, m.MountDir)
		}

	case "stop":
		if len(args) < 1 {
			fmt.Println("Usage: rfs stop <alias>[:<path>]")
			os.Exit(1)
		}
		name := resolveMountName(args[0])
		resp := sendCmd(Command{Type: "stop", Name: name})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		fmt.Println("Stopped:", args[0])

	case "logs":
		if len(args) < 1 {
			fmt.Println("Usage: rfs logs <alias>[:<path>]")
			os.Exit(1)
		}
		name := resolveMountName(args[0])
		logFile := filepath.Join(stateDir, "mounts", name+".log")
		f, err := os.Open(logFile)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		io.Copy(os.Stdout, f)
		f.Close()

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: rfs <command>")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  mount <alias>[:<path>]   Mount a remote directory")
	fmt.Println("  list                     List all mounts")
	fmt.Println("  stop <name>              Stop a mount")
	fmt.Println("  logs <name>              Show logs for a mount")
}

func parseTarget(target string) (alias, path string) {
	for i := 0; i < len(target); i++ {
		if target[i] == ':' {
			return target[:i], target[i+1:]
		}
	}
	return target, "~"
}

func resolveMountName(target string) string {
	alias, path := parseTarget(target)
	return mountName(alias, path)
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "start", "list", "stop", "logs":
			runCLI()
			return
		case "daemon":
			d := NewDaemon()
			if err := d.Start(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	printUsage()
	os.Exit(1)
}
