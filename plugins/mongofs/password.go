package mongofs

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/unxed/vtui"
)

// How a password gets to the server. It is never stored by f4: a connection
// string that names a user and gives no password (mongodb://user@host/db) makes
// the panel ask for it in a masked dialog, the first time it connects. The
// answer lives in memory, in the panel's connector, only as long as the panel
// (and its clones) does, so a reconnect after a dropped link does not ask
// again, and a wrong one is forgotten and asked for anew (three tries).
// A password in MONGODB_URI itself still works, but then it sits in the
// environment, which is why the dialog is the recommended way.

// maxPasswordTries bounds the prompts for one connection.
const maxPasswordTries = 3

var errPasswordNotEntered = errors.New("mongofs: no password was entered")

// passwordPrompt asks for the password of who ("user@host:port"); tests
// replace it.
var passwordPrompt = promptPassword

// connector makes the connections of one panel.
type connector struct {
	mu       sync.Mutex
	pass     string
	havePass bool
}

// open connects as MONGODB_URI says, asking for a missing password.
func (c *connector) open(ctx context.Context) (*conn, error) {
	raw := os.Getenv("MONGODB_URI")
	if raw == "" {
		raw = defaultURI
	}
	cfg, err := parseURI(raw)
	if err != nil {
		return nil, err
	}
	if cfg.user == "" || cfg.passSet {
		return dial(ctx, cfg)
	}
	who := cfg.user + "@" + cfg.addr
	for try := 0; try < maxPasswordTries; try++ {
		c.mu.Lock()
		pass, have := c.pass, c.havePass
		c.mu.Unlock()
		if !have {
			if pass, err = passwordPrompt(ctx, who); err != nil {
				return nil, err
			}
			if pass == "" {
				return nil, errPasswordNotEntered
			}
		}
		cfg.pass = pass
		conn, err := dial(ctx, cfg)
		if err == nil {
			c.mu.Lock()
			c.pass, c.havePass = pass, true
			c.mu.Unlock()
			return conn, nil
		}
		if !errors.Is(err, errAuth) {
			return nil, err
		}
		c.mu.Lock()
		c.pass, c.havePass = "", false
		c.mu.Unlock()
	}
	return nil, errAuth
}

type passwordResult struct {
	password string
	err      error
}

func promptPassword(ctx context.Context, who string) (string, error) {
	if vtui.FrameManager == nil {
		return "", errors.New("mongofs: cannot ask for a password without an active UI")
	}
	result := make(chan passwordResult, 1)
	vtui.FrameManager.PostTask(func() { showPasswordDialog(who, result) })
	select {
	case r := <-result:
		return r.password, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func showPasswordDialog(who string, result chan<- passwordResult) {
	title := mongoText("Mongo.PasswordTitle", "MongoDB password", "Пароль MongoDB") + ": " + who
	width := max(52, len([]rune(title))+6)
	dlg := vtui.NewCenteredDialog(width, 7, title)
	dlg.ShowClose = true
	x, y := dlg.X1+2, dlg.Y1+2
	password := vtui.NewPasswordEdit(x+12, y, width-16, "")
	dlg.AddItem(vtui.NewLabel(x, y, mongoText("Mongo.Password", "Password:", "Пароль:"), password))
	dlg.AddItem(password)
	ok := vtui.NewButton(dlg.X1+width/2-12, dlg.Y2-2, vtui.Msg("vtui.Ok"))
	ok.IsDefault = true
	cancel := vtui.NewButton(dlg.X1+width/2+1, dlg.Y2-2, vtui.Msg("vtui.Cancel"))
	dlg.AddItem(ok)
	dlg.AddItem(cancel)

	finished := false
	finish := func(r passwordResult) {
		if finished {
			return
		}
		finished = true
		result <- r
	}
	ok.OnClick = func() {
		finish(passwordResult{password: password.GetText()})
		password.SetText("")
		dlg.Close()
	}
	cancel.OnClick = func() { dlg.Close() }
	dlg.OnResult = func(code int) {
		if code < 0 {
			finish(passwordResult{err: context.Canceled})
		}
	}
	vtui.FrameManager.Push(dlg)
}
