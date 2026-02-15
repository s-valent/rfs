package ssh

import (
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type SSHClient struct {
	alias string
	conn  *ssh.Client
	mu    sync.Mutex
}

func Connect(alias string) (*SSHClient, error) {
	conn, err := getConn(alias)
	if err != nil {
		return nil, err
	}
	return &SSHClient{alias: alias, conn: conn}, nil
}

func (c *SSHClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *SSHClient) NewSession() (*ssh.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.NewSession()
	}
	return nil, fmt.Errorf("not connected")
}

func (c *SSHClient) reconnect() error {
	log.Printf("Attempting to reconnect to %s...", c.alias)

	for i := range 5 {
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

func (c *SSHClient) EnsureConnected() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	return c.reconnectNoLock()
}

func (c *SSHClient) reconnectNoLock() error {
	log.Printf("Attempting to reconnect to %s...", c.alias)

	for i := range 5 {
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

func (c *SSHClient) WithReconnect(fn func(*ssh.Client) error) error {
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

func (c *SSHClient) GetConn() *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *SSHClient) SetConn(conn *ssh.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

func (c *SSHClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}
