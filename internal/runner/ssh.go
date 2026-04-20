package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/TipoKrewaz/scadt/internal/models"
)

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

func Exec(ctx context.Context, cfg *models.SSHConfig, command string, timeout time.Duration) (*Result, error) {
	if cfg == nil {
		return nil, errors.New("ssh config is nil")
	}
	if cfg.Host == "" || cfg.User == "" {
		return nil, errors.New("ssh: host and user are required")
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	auths, err := buildAuth(cfg)
	if err != nil {
		return nil, err
	}
	hkCheck, err := buildHostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: hkCheck,
		Timeout:         timeout,
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientCfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case err = <-done:
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return nil, ctx.Err()
	case <-time.After(timeout):
		_ = session.Signal(ssh.SIGKILL)
		return nil, fmt.Errorf("ssh: timeout after %s", timeout)
	}

	res := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	if err == nil {
		res.ExitCode = 0
		return res, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitStatus()
		return res, nil // ненулевой exit это не ошибка транспорта
	}
	return res, fmt.Errorf("ssh run: %w", err)
}

func buildAuth(cfg *models.SSHConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if cfg.KeyFile != "" {
		pem, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("ssh: read key: %w", err)
		}
		var signer ssh.Signer
		if cfg.KeyPass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(cfg.KeyPass))
		} else {
			signer, err = ssh.ParsePrivateKey(pem)
		}
		if err != nil {
			return nil, fmt.Errorf("ssh: parse key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}
	if len(methods) == 0 {
		return nil, errors.New("ssh: no auth methods (provide key_file or password)")
	}
	return methods, nil
}

func buildHostKeyCallback(cfg *models.SSHConfig) (ssh.HostKeyCallback, error) {
	if cfg.HostKeyFP == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	expected := strings.TrimPrefix(strings.TrimSpace(cfg.HostKeyFP), "SHA256:")
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		sum := sha256.Sum256(key.Marshal())
		got := strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
		if got != expected {
			return fmt.Errorf("host key mismatch: expected SHA256:%s, got SHA256:%s", expected, got)
		}
		return nil
	}, nil
}
