package fusefs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// A Spec is a mount request in the one form every entry point speaks: the
// command line today, the UI command from iteration 2 on, an fstab line via
// the mount.fuse.f4 helper.
//
// It is deliberately plain data. A mount is a location, not a session (see
// "Ownership" in FUSE.md), and a request for one has to survive being handed
// to a different process — which is exactly what --daemon does with it.
type Spec struct {
	// Source is whatever the resolver can re-open: a path, a URI, an
	// archive file. Never a live VFS.
	Source string

	// MountPoint is empty when the caller wants the manager to derive one.
	MountPoint string

	ReadOnly   bool
	AllowOther bool

	// Daemon asks for the mount to outlive the invoking process.
	Daemon bool

	// JSON switches the command output to one machine-readable object.
	JSON bool

	// Timeout bounds how long the parent waits for a --daemon child to
	// report that it is mounted.
	Timeout time.Duration

	// Extra collects -o options this version does not recognise. They are
	// carried through so an fstab line does not have to be rewritten every
	// time an option is added.
	Extra []string
}

// DefaultReadyTimeout bounds the wait for a daemon child. A remote backend
// that has to authenticate can take seconds; one that hangs must not take
// the shell with it.
const DefaultReadyTimeout = 60 * time.Second

// ErrWriteNotImplemented is returned for --rw until iteration 4 lands. The
// flag is accepted and rejected rather than ignored: a mount that silently
// comes up read-only when the caller asked for write is a data-loss trap.
var ErrWriteNotImplemented = errors.New("read-write mounts are not implemented yet (see FUSE.md, iteration 4); mount read-only or use the panels")

// fstabNoise are mount(8) options that concern mount(8) and not us. They
// appear in every real fstab line and must not be treated as errors.
var fstabNoise = map[string]bool{
	"defaults": true, "auto": true, "noauto": true, "user": true,
	"users": true, "nouser": true, "owner": true, "group": true,
	"_netdev": true, "nofail": true, "nodev": true, "nosuid": true,
	"noexec": true, "exec": true, "dev": true, "suid": true,
	"relatime": true, "noatime": true, "async": true, "sync": true,
}

// NewSpec returns a Spec with the defaults the CLI and the UI share.
func NewSpec() *Spec {
	return &Spec{ReadOnly: true, Timeout: DefaultReadyTimeout}
}

// ApplyOption applies one mount(8)-style option, with or without a value.
func (s *Spec) ApplyOption(opt string) error {
	opt = strings.TrimSpace(opt)
	if opt == "" {
		return nil
	}
	key, val, hasVal := strings.Cut(opt, "=")
	switch key {
	case "ro":
		s.ReadOnly = true
	case "rw":
		s.ReadOnly = false
	case "allow_other":
		s.AllowOther = true
	case "mountpoint", "at":
		if !hasVal {
			return fmt.Errorf("option %q needs a value", key)
		}
		s.MountPoint = val
	case "timeout":
		if !hasVal {
			return fmt.Errorf("option %q needs a value", key)
		}
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("option timeout=%q: %w", val, err)
		}
		s.Timeout = d
	default:
		if fstabNoise[key] || strings.HasPrefix(key, "x-") || key == "comment" {
			return nil
		}
		s.Extra = append(s.Extra, opt)
	}
	return nil
}

// ApplyOptions applies a comma-separated -o string.
func (s *Spec) ApplyOptions(opts string) error {
	for _, o := range strings.Split(opts, ",") {
		if err := s.ApplyOption(o); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports whether this Spec can be served by the current build.
func (s *Spec) Validate() error {
	if strings.TrimSpace(s.Source) == "" {
		return errors.New("no mount source given")
	}
	if !s.ReadOnly {
		return ErrWriteNotImplemented
	}
	if s.Timeout <= 0 {
		s.Timeout = DefaultReadyTimeout
	}
	return nil
}

// ChildArgs rebuilds the argument vector for a --daemon child. It is
// generated from the Spec rather than copied from os.Args so that the child
// gets exactly the request the parent parsed, and so that the round trip can
// be tested without spawning anything.
func (s Spec) ChildArgs() []string {
	args := []string{"--mount", s.Source, "--foreground"}
	if s.MountPoint != "" {
		args = append(args, "--at", s.MountPoint)
	}
	if s.ReadOnly {
		args = append(args, "--ro")
	} else {
		args = append(args, "--rw")
	}
	if s.AllowOther {
		args = append(args, "--allow-other")
	}
	for _, e := range s.Extra {
		args = append(args, "-o", e)
	}
	return args
}

// Command is what an argument vector turned out to be asking for.
type Command int

const (
	// CmdNone means the arguments say nothing about mounting and f4
	// should start normally.
	CmdNone Command = iota
	CmdMount
	CmdUmount
	CmdList
)

// Exit codes. Small and documented rather than borrowed from sysexits,
// because the audience is shell scripts and mount(8), not C programs.
const (
	ExitOK          = 0
	ExitUsage       = 1
	ExitUnsupported = 2
	ExitFailed      = 3
	ExitBusy        = 4
	ExitNoSuchMount = 5
)

// fstabHelperNames are the argv[0] basenames under which mount(8) invokes
// us as `helper SOURCE MOUNTPOINT [-o opts]`.
var fstabHelperNames = map[string]bool{
	"mount.f4": true, "mount.fuse.f4": true,
}

// ParseArgs turns a full argument vector (including argv[0]) into a command.
//
// It returns CmdNone for every vector that does not mention mounting, so the
// caller can hand it straight to the normal startup path.
func ParseArgs(argv []string) (Command, *Spec, error) {
	if len(argv) == 0 {
		return CmdNone, nil, nil
	}
	if fstabHelperNames[filepath.Base(argv[0])] {
		return parseFstabArgs(argv[1:])
	}
	return parseFlagArgs(argv[1:])
}

func parseFstabArgs(args []string) (Command, *Spec, error) {
	spec := NewSpec()
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 >= len(args) {
				return CmdMount, nil, errors.New("-o needs a value")
			}
			i++
			if err := spec.ApplyOptions(args[i]); err != nil {
				return CmdMount, nil, err
			}
		case "-n", "-v", "-s", "-f":
			// mount(8) niceties we have nothing to do with.
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 2 {
		return CmdMount, nil, errors.New("usage: mount.fuse.f4 SOURCE MOUNTPOINT [-o options]")
	}
	spec.Source = positional[0]
	spec.MountPoint = positional[1]
	// fstab entries are expected to come up in the background.
	spec.Daemon = true
	return CmdMount, spec, nil
}

func parseFlagArgs(args []string) (Command, *Spec, error) {
	cmd := CmdNone
	spec := NewSpec()
	var target string
	var positional []string
	foreground := false

	setCmd := func(c Command) error {
		if cmd != CmdNone && cmd != c {
			return errors.New("--mount, --umount and --list-mounts are mutually exclusive")
		}
		cmd = c
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		// value returns the option's argument, inline or next.
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "--mount":
			if err := setCmd(CmdMount); err != nil {
				return cmd, nil, err
			}
			v, err := value()
			if err != nil {
				return cmd, nil, err
			}
			spec.Source = v
		case "--umount", "--unmount":
			if err := setCmd(CmdUmount); err != nil {
				return cmd, nil, err
			}
			v, err := value()
			if err != nil {
				return cmd, nil, err
			}
			target = v
		case "--list-mounts":
			if err := setCmd(CmdList); err != nil {
				return cmd, nil, err
			}
		case "--at", "--mountpoint":
			v, err := value()
			if err != nil {
				return cmd, nil, err
			}
			spec.MountPoint = v
		case "-o":
			v, err := value()
			if err != nil {
				return cmd, nil, err
			}
			if err := spec.ApplyOptions(v); err != nil {
				return cmd, nil, err
			}
		case "--timeout":
			v, err := value()
			if err != nil {
				return cmd, nil, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return cmd, nil, fmt.Errorf("--timeout %q: %w", v, err)
			}
			spec.Timeout = d
		case "--ro", "--read-only":
			spec.ReadOnly = true
		case "--rw", "--read-write":
			spec.ReadOnly = false
		case "--allow-other":
			spec.AllowOther = true
		case "--daemon":
			spec.Daemon = true
		case "--foreground":
			foreground = true
		case "--json":
			spec.JSON = true
		default:
			if cmd != CmdNone && !strings.HasPrefix(arg, "-") {
				positional = append(positional, arg)
			}
			// Anything else belongs to f4 proper; ignore it here.
		}
	}

	if foreground {
		spec.Daemon = false
	}

	switch cmd {
	case CmdMount:
		if spec.MountPoint == "" && len(positional) > 0 {
			spec.MountPoint = positional[0]
		}
	case CmdUmount:
		spec.Source = target
	}
	return cmd, spec, nil
}
