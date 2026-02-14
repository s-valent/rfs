package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ssh "remote-fs/ssh"

	"github.com/smallfz/libnfs-go/auth"
	"github.com/smallfz/libnfs-go/backend"
	nfsFs "github.com/smallfz/libnfs-go/fs"
	nfslog "github.com/smallfz/libnfs-go/log"
	"github.com/smallfz/libnfs-go/server"
)

func findFreePort() (string, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	defer l.Close()

	addr := l.Addr().(*net.TCPAddr)
	return fmt.Sprintf(":%d", addr.Port), nil
}

func main() {
	nfslog.SetLevelName("info")

	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Printf("Usage: %s <ssh alias>:<remote path> [mount path]\n", os.Args[0])
		fmt.Printf("  <ssh alias>    SSH alias from ~/.ssh/config\n")
		fmt.Printf("  <remote path> Remote directory to mount (supports ~/ prefix)\n")
		fmt.Printf("  [mount path]  Local mount point (optional, default: /tmp/remote-fs-<random>)\n")
		os.Exit(1)
	}

	target := args[0]

	var sshAlias, remotePath string
	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		sshAlias = parts[0]
		remotePath = parts[1]
	} else {
		sshAlias = target
		remotePath = "/"
	}

	var mountDir string
	if len(args) >= 2 {
		mountDir = args[1]
	} else {
		safePath := strings.ReplaceAll(remotePath, "/", ":")
		tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("%s %s", sshAlias, safePath))
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			log.Fatalf("Failed to create temp mount directory: %v", err)
		}
		mountDir = tmpDir
	}

	listen, err := findFreePort()
	if err != nil {
		log.Fatalf("Failed to find free port: %v", err)
	}

	log.Printf("Starting remote-fs:\n")
	log.Printf("  SSH alias:     %s\n", sshAlias)
	log.Printf("  Remote path:  %s\n", remotePath)
	log.Printf("  Mount point:  %s\n", mountDir)
	log.Printf("  Listen:       %s\n", listen)

	mounted := false
	cleanup := func() {
		if mounted {
			log.Printf("Unmounting %s...\n", mountDir)
			cmd := exec.Command("umount", mountDir)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Printf("Warning: unmount failed: %v\n", err)
			}
		}
		if _, err := os.Stat(mountDir); err == nil {
			log.Printf("Removing mount directory %s...\n", mountDir)
			if err := os.Remove(mountDir); err != nil {
				log.Printf("Warning: failed to remove mount dir: %v\n", err)
			}
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received signal, shutting down...")
		cleanup()
		os.Exit(0)
	}()

	if err := os.MkdirAll(mountDir, 0755); err != nil {
		log.Fatalf("Failed to create mount directory: %v", err)
	}
	log.Printf("Created mount directory: %s\n", mountDir)

	client, err := ssh.Connect(sshAlias)
	if err != nil {
		cleanup()
		log.Fatalf("Failed to dial: %v", err)
	}
	defer client.Close()

	log.Printf("Successfully connected to %s\n", sshAlias)

	session, err := client.NewSession()
	if err != nil {
		cleanup()
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if err := session.Run("echo \"($SHELL) $(pwd) $(whoami)\""); err != nil {
		cleanup()
		log.Fatal("Failed to run command: ", err)
	}

	fs, err := client.NewFS(remotePath)
	if err != nil {
		cleanup()
		log.Fatalf("Failed to create SFTP client: %v", err)
	}
	defer fs.Close()

	backend := backend.New(func() nfsFs.FS { return fs }, auth.Null)

	svr, err := server.NewServerTCP(listen, backend)
	if err != nil {
		cleanup()
		log.Fatalf("server.NewServerTCP: %v", err)
	}

	go func() {
		if err := svr.Serve(); err != nil {
			log.Fatalf("svr.Serve: %v", err)
		}
	}()

	log.Printf("Waiting for server to start...\n")
	time.Sleep(2 * time.Second)

	log.Printf("Unmounting any existing mount...\n")
	exec.Command("umount", "-f", mountDir).Run()

	log.Printf("Mounting NFS...\n")
	port := strings.TrimPrefix(listen, ":")
	mountCmd := exec.Command("mount", "-o", fmt.Sprintf("nfsvers=4,soft,noacl,tcp,port=%s", port), "-t", "nfs", "localhost:/", mountDir)
	mountCmd.Stdout = os.Stdout
	mountCmd.Stderr = os.Stderr
	if err := mountCmd.Run(); err != nil {
		log.Printf("Mount failed: %v\n", err)
		log.Printf("Trying to unmount if already mounted...")
		exec.Command("umount", mountDir).Run()
		if err := mountCmd.Run(); err != nil {
			cleanup()
			log.Fatalf("Mount failed: %v\n", err)
		}
	}
	mounted = true
	log.Printf("Mounted at %s\n", mountDir)

	log.Printf("Server running.\n")
	fmt.Printf("\nMount point: %s\n", mountDir)
	fmt.Printf("NFS:         localhost%s\n", listen)

	select {}
}
