package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const maxPasswordBytes = 4096

func (opts *options) loginCommand() *cobra.Command {
	var email, passwordFile string
	cmd := &cobra.Command{
		Use:     "login",
		Short:   "Sign in and save a session for the selected Admin origin.",
		Example: "  ownsolid login\n  ownsolid login --email \"$ADMIN_EMAIL\" --password-file \"$PRIVATE_FILE\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := opts.settings(cmd)
			if err != nil {
				return err
			}
			return login(cmd, cfg, email, passwordFile)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Administrator email address")
	cmd.Flags().StringVar(&passwordFile, "password-file", "", "Read the password from a mode 0600 file")
	return cmd
}

func login(cmd *cobra.Command, cfg settings, email, passwordFile string) error {
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	email, password, err := loginInput(cmd, email, passwordFile)
	if err != nil {
		return err
	}
	defer clear(password)
	store, err := openSessionStore(cfg, true)
	if err != nil {
		return err
	}
	defer store.root.Close()
	lock, err := store.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	previous, err := store.read()
	if err != nil {
		return err
	}
	client.restoreSession(previous)
	body, err := json.Marshal(map[string]string{"email": email, "password": string(password)})
	if err != nil {
		return errors.New("cannot encode the login request")
	}
	defer clear(body)

	var result struct {
		Audience string `json:"audience"`
	}
	ctx := cmd.Context()
	if err := client.authPost(ctx, "login", body, &result); err != nil {
		return err
	}
	value := client.cookie("session")
	if result.Audience != "admin" || value == previous || !validSession(value) {
		return fmt.Errorf("server %s did not issue a new Admin session; check that --server points to the Admin entrance", cfg.origin.String())
	}
	if err := store.save(value); err != nil {
		revokeErr := client.authPost(context.WithoutCancel(ctx), "logout", nil, nil)
		if revokeErr != nil {
			revokeErr = fmt.Errorf("could not revoke the newly issued session: %w", revokeErr)
		}
		return errors.Join(fmt.Errorf("save the Admin session; check the config directory and retry: %w", err), revokeErr)
	}
	return authResult(cmd, "signed in", cfg)
}

func loginInput(cmd *cobra.Command, email, passwordFile string) (string, []byte, error) {
	email = strings.TrimSpace(email)
	var password []byte
	switch {
	case passwordFile != "":
		if email == "" {
			return "", nil, errors.New("--password-file requires --email")
		}
		file, err := os.OpenFile(passwordFile, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return "", nil, fmt.Errorf("open the password file: %w", err)
		}
		defer file.Close()
		if err := privateFile(file); err != nil {
			return "", nil, fmt.Errorf("inspect the password file: %w", err)
		}
		password, err = io.ReadAll(io.LimitReader(file, maxPasswordBytes+3))
		if err != nil {
			return "", nil, fmt.Errorf("read the password file: %w", err)
		}
		if bytes.HasSuffix(password, []byte{'\n'}) {
			password = bytes.TrimSuffix(password[:len(password)-1], []byte{'\r'})
		}
	default:
		var err error
		email, password, err = promptLogin(cmd, email)
		if err != nil {
			return "", nil, err
		}
	}
	if email == "" || len(password) == 0 || len(password) > maxPasswordBytes {
		clear(password)
		return "", nil, errors.New("enter an email and a nonempty password of at most 4096 bytes")
	}
	return email, password, nil
}

func promptLogin(cmd *cobra.Command, email string) (string, []byte, error) {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return "", nil, errors.New("login needs an interactive terminal or --email and --password-file")
	}
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", nil, fmt.Errorf("prepare the terminal for hidden password input: %w", err)
	}
	defer term.Restore(fd, state)
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{in, cmd.ErrOrStderr()}, "Email: ")
	if email == "" {
		value, err := terminal.ReadLine()
		if err != nil {
			return "", nil, errors.New("could not read the email; run ownsolid login to retry")
		}
		email = strings.TrimSpace(value)
	}
	password, err := terminal.ReadPassword("Password: ")
	if err != nil {
		return "", nil, errors.New("could not read the password; run ownsolid login to retry")
	}
	return email, []byte(password), nil
}

func (opts *options) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Revoke the saved Admin session and remove its local credential.",
		Example: "  ownsolid logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := opts.settings(cmd)
			if err != nil {
				return err
			}
			return logout(cmd, cfg)
		},
	}
}

func logout(cmd *cobra.Command, cfg settings) error {
	store, err := openSessionStore(cfg, false)
	if errors.Is(err, os.ErrNotExist) {
		return authResult(cmd, "already signed out", cfg)
	}
	if err != nil {
		return err
	}
	defer store.root.Close()
	lock, err := store.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	value, err := store.read()
	if err != nil {
		return err
	}
	if value == "" {
		return authResult(cmd, "already signed out", cfg)
	}
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	client.restoreSession(value)
	if err := client.authPost(cmd.Context(), "logout", nil, nil); err != nil {
		return fmt.Errorf("logout did not complete; the local session was kept so you can retry: %w", err)
	}
	if err := store.remove(); err != nil {
		return err
	}
	return authResult(cmd, "signed out", cfg)
}

func authResult(cmd *cobra.Command, status string, cfg settings) error {
	return renderValue(cmd, []column{{"STATUS", "status"}, {"SERVER", "server"}}, map[string]string{
		"status": status, "server": cfg.origin.String(),
	})
}
