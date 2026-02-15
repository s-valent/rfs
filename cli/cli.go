package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var stateDir string
var binaryName string

func init() {
	home, _ := os.UserHomeDir()
	stateDir = filepath.Join(home, ".local", "rfs")
	binaryName = filepath.Base(os.Args[0])
	if binaryName == "." || binaryName == "" {
		binaryName = "rfs"
	}
}

type Command struct {
	Type       string   `json:"type"`
	Names      []string `json:"names,omitempty"`
	SSHAlias   string   `json:"sshAlias"`
	RemotePath string   `json:"remotePath"`
	MountDir   string   `json:"mountDir,omitempty"`
}

type Response struct {
	OK     bool         `json:"ok"`
	Error  string       `json:"error,omitempty"`
	Mount  *MountInfo   `json:"mount,omitempty"`
	Mounts []*MountInfo `json:"mounts,omitempty"`
	Names  []string     `json:"names,omitempty"`
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

func StateDir() string {
	return stateDir
}

func BinaryName() string {
	return binaryName
}

func connect() net.Conn {
	socketPath := filepath.Join(stateDir, "daemon.sock")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		startDaemon()
		for range 20 {
			time.Sleep(50 * time.Millisecond)
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

func SendCmd(cmd Command) *Response {
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

func RunCLI() {
	args := os.Args[1:]
	if len(args) < 1 {
		PrintUsage()
		os.Exit(1)
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "up":
		if (len(args) < 1) || (len(args) > 2) {
			fmt.Println("Usage:", binaryName, "up <alias>[:<path>] [mountpoint]")
			os.Exit(1)
		}
		alias, path := ParseTarget(args[0])
		mountDir := ""
		if len(args) == 2 {
			mountDir = args[1]
		}
		resp := SendCmd(Command{Type: "up", SSHAlias: alias, RemotePath: path, MountDir: mountDir})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		fmt.Printf("%s:%s  port:%s  %s\n", resp.Mount.SSHAlias, resp.Mount.RemotePath, resp.Mount.Port, resp.Mount.MountDir)

	case "ls":
		resp := SendCmd(Command{Type: "ls"})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		if len(resp.Mounts) == 0 {
			fmt.Println("No mounts")
			return
		}
		fmt.Printf("%-20s %-6s %s\n", "ALIAS:PATH", "PORT", "MOUNT")
		for _, m := range resp.Mounts {
			fmt.Printf("%-20s %-6s %s\n", m.SSHAlias+":"+m.RemotePath, m.Port, m.MountDir)
		}

	case "down":
		var names []string
		for _, arg := range args {
			names = append(names, ResolveMountName(arg))
		}
		resp := SendCmd(Command{Type: "down", Names: names})
		if resp.Error != "" {
			fmt.Println("Error:", resp.Error)
			os.Exit(1)
		}
		for _, n := range resp.Names {
			fmt.Println(n, "stopped")
		}

	case "logs":
		if len(args) != 1 {
			fmt.Println("Usage:", binaryName, "logs <alias>[:<path>]")
			os.Exit(1)
		}
		name := ResolveMountName(args[0])
		logFile := filepath.Join(stateDir, "mounts", name+".log")
		f, err := os.Open(logFile)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		io.Copy(os.Stdout, f)
		f.Close()

	default:
		PrintUsage()
		os.Exit(1)
	}
}

func PrintUsage() {
	fmt.Println("Usage:", binaryName, "<command>")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  up <alias>[:<path>] [mountpoint]   Mount a remote directory")
	fmt.Println("  ls                                 List all mounts")
	fmt.Println("  down <alias>[:<path>]              Stop a mount")
	fmt.Println("  logs <alias>[:<path>]              Show logs for a mount")
}

func ParseTarget(target string) (alias, path string) {
	target = strings.TrimRight(target, "/")
	for i := 0; i < len(target); i++ {
		if target[i] == ':' {
			return target[:i], target[i+1:]
		}
	}
	return target, "~"
}

func MountName(alias, path string) string {
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

func ResolveMountName(target string) string {
	alias, path := ParseTarget(target)
	return MountName(alias, path)
}
