package ssh

import (
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshClient struct {
	alias string
	conn  *ssh.Client
	mu    sync.Mutex
}

func Connect(alias string) (*sshClient, error) {
	conn, err := getConn(alias)
	if err != nil {
		return nil, err
	}
	return &sshClient{alias: alias, conn: conn}, nil
}

func (c *sshClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *sshClient) NewSession() (*ssh.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.NewSession()
	}
	return nil, fmt.Errorf("not connected")
}

func (c *sshClient) reconnect() error {
	log.Printf("Attempting to reconnect to %s...", c.alias)

	for i := 0; i < 5; i++ {
		conn, err := getConn(c.alias)
		if err == nil {
			c.conn = conn
			log.Printf("Reconnected to %s successfully", c.alias)
			return nil
		}

		log.Printf("Reconnection attempt %d failed: %v", i+1, err)
		waitTime := time.Duration(i+1) * 2 * time.Second
		log.Printf("Waiting %v before retry...", waitTime)
		time.Sleep(waitTime)
	}

	return fmt.Errorf("failed to reconnect after 5 attempts")
}

func (c *sshClient) EnsureConnected() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	return c.reconnectNoLock()
}

func (c *sshClient) reconnectNoLock() error {
	log.Printf("Attempting to reconnect to %s...", c.alias)

	for i := 0; i < 5; i++ {
		conn, err := getConn(c.alias)
		if err == nil {
			c.conn = conn
			log.Printf("Reconnected to %s successfully", c.alias)
			return nil
		}

		log.Printf("Reconnection attempt %d failed: %v", i+1, err)
		waitTime := time.Duration(i+1) * 2 * time.Second
		log.Printf("Waiting %v before retry...", waitTime)
		time.Sleep(waitTime)
	}

	return fmt.Errorf("failed to reconnect after 5 attempts")
}

func (c *sshClient) WithReconnect(fn func(*ssh.Client) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.reconnectNoLock(); err != nil {
			return err
		}
	}

	err := fn(c.conn)
	if err != nil {
		log.Printf("Operation failed, attempting reconnect: %v", err)
		c.conn = nil
		if reerr := c.reconnectNoLock(); reerr != nil {
			return fmt.Errorf("operation failed: %v, reconnection also failed: %w", err, reerr)
		}

		return fn(c.conn)
	}

	return nil
}

func (c *sshClient) GetConn() *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *sshClient) SetConn(conn *ssh.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}
