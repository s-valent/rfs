// mount -o nfsvers=4,soft,noacl,tcp -t nfs localhost:/ /path/to/dir

package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	ssh "remote-fs/ssh"

	"github.com/smallfz/libnfs-go/auth"
	"github.com/smallfz/libnfs-go/backend"
	nfsFs "github.com/smallfz/libnfs-go/fs"
	"github.com/smallfz/libnfs-go/memfs"
	"github.com/smallfz/libnfs-go/server"
)

func main() {
	var hostAlias string
	listen := ":2049"
	flag.StringVar(&listen, "l", listen, "Server listen address")
	flag.StringVar(&hostAlias, "h", hostAlias, "SSH host alias")
	flag.Parse()

	client, err := ssh.Connect(hostAlias)
	if err != nil {
		log.Fatalf("Failed to dial: %v", err)
	}
	defer client.Close()

	log.Printf("Successfully connected to %v\n", hostAlias)

	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if err := session.Run("echo \"($SHELL) $(pwd) $(whoami)\""); err != nil {
		log.Fatal("Failed to run command: ", err)
	}

	fs, err := client.NewFS()
	if err != nil {
		log.Fatalf("Failed to create SFTP client: %v", err)
	}
	defer fs.Close()

	file, err := fs.Create("/home/valent/hello.txt")
	if err != nil {
		file.Close()
	}

	mfs := memfs.NewMemFS()

	// We don't need to create a new fs for each connection as memfs is opaque towards SetCreds.
	// If the file system would depend on SetCreds, make sure to generate a new fs.FS for each connection.
	backend := backend.New(func() nfsFs.FS { return fs }, auth.Null)

	perm := os.FileMode(0o755)
	mfs.MkdirAll("/mount", os.FileMode(0o755))
	mfs.MkdirAll("/test", os.FileMode(0o755))
	mfs.MkdirAll("/test2", os.FileMode(0o755))
	mfs.MkdirAll("/many", os.FileMode(0o755))

	f, err := mfs.OpenFile("/mount/hello.txt", os.O_CREATE, perm)
	if err != nil {
		log.Fatalf("error mounting file, %v", err)
	}

	f.Write([]byte("hello\n"))
	f.Close()

	for i := range 256 {
		mfs.MkdirAll(fmt.Sprintf("/many/sub-%d", i+1), perm)
	}

	svr, err := server.NewServerTCP(listen, backend)
	if err != nil {
		log.Fatalf("server.NewServerTCP: %v", err)
	}

	if err := svr.Serve(); err != nil {
		log.Fatalf("svr.Serve: %v", err)
	}
}
