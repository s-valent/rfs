package ssh

import (
	"golang.org/x/crypto/ssh"
)

type sshClient struct {
	conn *ssh.Client
}

func Connect(alias string) (*sshClient, error) {
	conn, err := getConn(alias)
	if err != nil {
		return nil, err
	}
	return &sshClient{conn}, nil
}

func (c *sshClient) Close() error {
	return c.conn.Close()
}

func (c *sshClient) NewSession() (*ssh.Session, error) {
	return c.conn.NewSession()
}
