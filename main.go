// mount -o nfsvers=4,soft,noacl,tcp -t nfs localhost:/ /path/to/dir

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	ssh "remote-fs/ssh"

	"github.com/smallfz/libnfs-go/auth"
	"github.com/smallfz/libnfs-go/backend"
	nfsFs "github.com/smallfz/libnfs-go/fs"
	"github.com/smallfz/libnfs-go/server"
)

func main() {
	var hostAlias string
	var rootDir string
	var mountDir string
	listen := ":2049"
	flag.StringVar(&listen, "l", listen, "Server listen address")
	flag.StringVar(&hostAlias, "h", hostAlias, "SSH host alias")
	flag.StringVar(&rootDir, "r", "/", "SFTP root directory")
	flag.StringVar(&mountDir, "m", "/tmp/remote-fs-mount", "Mount directory")
	flag.Parse()

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

	client, err := ssh.Connect(hostAlias)
	if err != nil {
		cleanup()
		log.Fatalf("Failed to dial: %v", err)
	}
	defer client.Close()

	log.Printf("Successfully connected to %v\n", hostAlias)

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

	fs, err := client.NewFS(rootDir)
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
	mountSrc := "localhost:/"
	mountCmd := exec.Command("mount", "-o", fmt.Sprintf("nfsvers=4,soft,noacl,tcp,port=%s", strings.TrimPrefix(listen, ":")), "-t", "nfs", mountSrc, mountDir)
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

	log.Printf("Server running. Mount point: %s\n", mountDir)
	fmt.Printf("Mount point: %s\n", mountDir)

	select {}
}
