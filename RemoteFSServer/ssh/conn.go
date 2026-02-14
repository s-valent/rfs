package ssh

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type sshConfig struct {
	user     string
	hostname string
	port     string
	signers  []ssh.Signer
	agent    string
}

func getConfig(alias string) (c sshConfig, err error) {
	c.agent = os.Getenv("SSH_AUTH_SOCK")

	var keys []string
	var keyErrs []string

	out, err := exec.Command("ssh", "-G", alias).Output()
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if key == "user" {
			c.user = value
		} else if key == "hostname" {
			c.hostname = value
		} else if key == "port" {
			c.port = value
		} else if key == "identityfile" {
			path, err := normalizePath(value)
			if err != nil {
				keyErrs = append(keyErrs, fmt.Sprintf("%v: failed to normalize: %v", value, err))
				continue
			}
			key, err := os.ReadFile(path)
			if err != nil {
				keyErrs = append(keyErrs, fmt.Sprintf("%v: failed to read: %v", value, err))
				continue
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				keyErrs = append(keyErrs, fmt.Sprintf("%v: failed to parse: %v", value, err))
				continue
			}
			keys = append(keys, value)
			c.signers = append(c.signers, signer)
		} else if key == "identityagent" {
			c.agent = value
		}
	}

	log.Printf("Parsed confing for %v: %v@%v:%v, found identity agent %v and keys [%v], errors: [%v]",
		alias, c.user, c.hostname, c.port, c.agent, strings.Join(keys, ", "), strings.Join(keyErrs, "; "))

	return c, err
}

func getAgentSigners(sock string) ([]ssh.Signer, error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		return nil, err
	}
	return signers, nil
}

func normalizePath(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	path = filepath.Join(home, path[2:])
	return path, err
}

func getConn(alias string) (*ssh.Client, error) {
	aliasConfig, err := getConfig(alias)
	if err != nil {
		log.Fatalf("Failed to find config for alias %v", alias)
	}

	agentConn, err := net.Dial("unix", aliasConfig.agent)
	var agentSigners []ssh.Signer

	if err != nil {
		log.Printf("Failed to dial agent: %v", err)
		agentSigners = []ssh.Signer{}
	} else {
		defer agentConn.Close()
		agentClient := agent.NewClient(agentConn)
		agentSigners, err = agentClient.Signers()
		if err != nil {
			log.Printf("Failed to get agent signers: %v", err)
		}
	}

	signers := append(aliasConfig.signers, agentSigners...)

	knownHostsPath := os.ExpandEnv("$HOME/.ssh/known_hosts")
	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		log.Fatalf("Warning: failed to load known_hosts %v: %v", knownHostsPath, err)
	}

	config := &ssh.ClientConfig{
		User: aliasConfig.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signers...),
		},
		HostKeyCallback: hostKeyCallback,
	}

	addr := net.JoinHostPort(aliasConfig.hostname, aliasConfig.port)
	client, err := ssh.Dial("tcp", addr, config)
	return client, err
}
