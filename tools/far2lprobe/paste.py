#!/usr/bin/env python3
"""Diagnostic probe for the audit of unxed/f4#2115 (part 4B). Not for merging.

Plays the terminal for f4 in a PTY of 100x30 (answers CSI 5 n, 6 n, c), sends pastes
as the terminal's paste key does (ESC[200~ ... ESC[201~) and prints what came of them.
"""
import os, pty, re, select, signal, struct, sys, time, fcntl, termios, tempfile, glob
import pyte

F4 = sys.argv[1]
ONLY = sys.argv[2:] 

class Run:
    def __init__(self, files=None, edit=None):
        self.home = tempfile.mkdtemp(prefix='f4paste')
        self.work = self.home + '/work'
        os.makedirs(self.work)
        for n, c in (files or {}).items():
            open(self.work + '/' + n, 'w').write(c)
        env = dict(os.environ, HOME=self.home, TMPDIR=self.home, TERM='xterm-256color', XDG_CONFIG_HOME=self.home + '/.config',
                   LANG='C.UTF-8', SHELL='/bin/bash')
        for k in ('DISPLAY', 'WAYLAND_DISPLAY', 'SSH_CONNECTION', 'TMUX'):
            env.pop(k, None)
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.chdir(self.work)
            os.execve(F4, [F4, '--tty'] + os.environ.get('F4ARGS', '').split() + (['-e', edit] if edit else []), env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 100, 0, 0))
        self.screen = pyte.Screen(100, 30)
        self.stream = pyte.ByteStream(self.screen)
        self.seen = 0
        self.raw = bytearray()
        self.pump(4)

    def pump(self, t, quiet=None):
        end = time.time() + t
        last = time.time()
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if self.fd in r:
                try:
                    d = os.read(self.fd, 262144)
                except OSError:
                    return
                if not d:
                    return
                self.raw.extend(d)
                self.stream.feed(d)
                last = time.time()
                self.answer()
            elif quiet and time.time() - last > quiet:
                return

    def answer(self):
        data = bytes(self.raw[self.seen:])
        for m in re.finditer(rb'\x1b\[5n|\x1b\[6n|\x1b\[c|\x1b\[>c', data):
            s = m.group(0)
            os.write(self.fd, b'\x1b[0n' if s == b'\x1b[5n' else b'\x1b[1;1R' if s == b'\x1b[6n' else b'\x1b[?62;c')
        self.seen = len(self.raw)

    def send(self, b, wait=1.0):
        # write and read at the same time: f4 redraws while it takes the input, and a full
        # PTY in one direction must not stop the other
        i = 0
        t_end = time.time() + 120
        while i < len(b) and time.time() < t_end:
            r, w, _ = select.select([self.fd], [self.fd], [], 0.05)
            if self.fd in r:
                try:
                    d = os.read(self.fd, 262144)
                except OSError:
                    return
                self.raw.extend(d)
                self.stream.feed(d)
                self.answer()
            if self.fd in w:
                try:
                    i += os.write(self.fd, b[i:i + 4096])
                except BlockingIOError:
                    pass
        self.pump(wait, quiet=wait)

    def paste(self, text, wait=2.0):
        t0 = time.time()
        self.send(b'\x1b[200~' + text + b'\x1b[201~', wait=wait)
        return time.time() - t0

    def show(self, label, n=8, top=False):
        lines = [l.rstrip() for l in self.screen.display]
        print('SCREEN[%s]:' % label)
        for l in (lines[:n] if top else lines[-n:]):
            print('   |' + l)

    def has(self, s):
        return any(s in l for l in self.screen.display)

    def kill(self):
        try:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        except OSError:
            pass

F4_SHIFT = b'\x1b[1;2S'     # Shift+F4: edit a file by name
F2 = b'\x1bOQ'; F10 = b'\x1b[21~'; CTRLZ = b'\x1a'; ESC = b'\x1b'
CTRL_A = b'\x01'; CTRL_INS = b'\x1b[2;5~'; SHIFT_INS = b'\x1b[2;2~'



def scenario(name):
    return not ONLY or name in ONLY

lines2000 = ''.join('%d\n' % i for i in range(1, 2001)).encode()

if scenario('B01'):
    r = Run({'text.txt': 'line 1\nline 2\n'}, edit='text.txt'); r.show('editor opened', 4, top=True)
    dt = r.paste(lines2000, 3)
    r.show('after paste 2000 lines', 6, top=True)
    r.send(CTRLZ, 1.5)
    r.show('after one Ctrl+Z', 6, top=True)
    r.send(F2, 1.0); r.send(F10, 1.0)
    print('B01 file after undo+save:', repr(open(r.work + '/text.txt').read()[:60]))
    r.kill()

def edit_result(label, text, name='x.txt', pre=None, undo=False):
    r = Run(edit=name)
    dt = r.paste(text, 8)
    r.show(label + ' after paste', 5, top=True)
    if undo:
        r.send(CTRLZ, 1.0)
    r.send(F2, 3.0)
    r.show(label + ' after F2', 5, top=True)
    r.send(F10, 1.0)
    p = r.work + '/' + name
    data = open(p, 'rb').read() if os.path.exists(p) else b'<no file>'
    print('%s: %.1f s, %d bytes, %d lines, %d CR, %d empty lines, head=%r' % (
        label, dt, len(data), data.count(b'\n'), data.count(b'\r'),
        sum(1 for l in data.split(b'\n')[:-1] if not l), data[:40]))
    r.kill()
    return data

if scenario('B02'):
    d = edit_result('B02 auto-indent', b'a\n  b\n    c\n')
    print('B02 exact:', d == b'a\n  b\n    c\n')
if scenario('B06'):
    edit_result('B06 LF', lines2000)
    edit_result('B06 CR', lines2000.replace(b'\n', b'\r'))
    edit_result('B06 CRLF', lines2000.replace(b'\n', b'\r\n'))
if scenario('B07'):
    big = ''.join('%d\n' % i for i in range(1, 150001)).encode()
    edit_result('B07 1 MiB into the editor', big)

def cmd_paste(label, text, check=None, wait=2.0):
    r = Run()
    dt = r.paste(text, wait)
    r.show(label)
    r.send(b'', 1.5)
    if check:
        check(r)
    r.kill()
    return r, dt

if scenario('B03'):
    d = '/tmp/fcB'
    os.makedirs(d, exist_ok=True)
    for f in glob.glob(d + '/B03*'):
        os.unlink(f)
    def chk(r):
        print('B03 files created before any answer:', sorted(os.path.basename(x) for x in glob.glob(d + '/B03*')))
    cmd_paste('B03 two touch lines pasted into the command line', b'touch /tmp/fcB/B03a\ntouch /tmp/fcB/B03b', chk, 3)
    def chk2(r):
        print('B03 (CR separated) files created:', sorted(os.path.basename(x) for x in glob.glob(d + '/B03*')))
    cmd_paste('B03 CR-separated', b'touch /tmp/fcB/B03c\rtouch /tmp/fcB/B03d', chk2, 3)
if scenario('B04'):
    r = Run()
    r.paste(b'+-*\r\x1b[A\rend', 3); r.send(b'', 1)
    r.show('B04 command line', 12)
    print('B04 selection-like dialog on screen:', any(('elect' in l or 'Mask' in l) for l in r.screen.display[:-2]))
    r.kill()
    r = Run(edit='y.txt'); r.paste(b'+-*\r\x1b[A\rend\r', 3); r.show('B04 editor after paste', 6, top=True)
    r.send(F2, 2); r.send(F10, 1)
    print('B04 editor file:', repr(open(r.work + '/y.txt','rb').read()))
    r.kill()
if scenario('B05'):
    cmd_paste('B05 echo pasted with the panels active', b'echo pasted')
if scenario('B07c'):
    line = b'x' * 102400 + b'END'
    r = Run()
    dt = r.paste(line, 3)
    print('B07 100 KiB single line into the command line: paste+settle %.1f s, tail visible: %s' % (dt, r.has('END')))
    r.kill()
if scenario('B08'):
    d = '/tmp/fcB'
    os.makedirs(d, exist_ok=True)
    for f in glob.glob(d + '/B08*'):
        os.unlink(f)
    r = Run(edit='z.txt')
    r.paste(b'touch /tmp/fcB/B08a\rtouch /tmp/fcB/B08b\r', 3)
    r.show('B08 editor after paste (CR separated)', 6, top=True)
    r.send(CTRL_A, 0.5); r.send(CTRL_INS, 1.0)
    r.send(b'\x1b[1;5H', 0.5)            # Ctrl+Home
    r.send(SHIFT_INS, 2.0)
    r.show('B08 editor after Shift+Ins at the top', 8, top=True)
    r.send(CTRLZ, 1.0)
    r.show('B08 editor after one Ctrl+Z', 8, top=True)
    r.send(F2, 2.0)
    print('B08 editor file:', repr(open(r.work + '/z.txt','rb').read()))
    r.send(F10, 1.5)
    r.show('B08 after F10', 6)
    r.send(SHIFT_INS, 3.0)
    r.show('B08 Shift+Ins in the command line', 6)
    print('B08 files created:', sorted(os.path.basename(x) for x in glob.glob(d + '/B08*')))
    r.kill()

if scenario('P2000'):
    r = Run(edit='x.txt')
    t0 = time.time()
    r.send(b'\x1b[200~' + lines2000 + b'\x1b[201~', wait=1)
    print('P2000 paste of 2000 lines sent in %.1f s' % (time.time() - t0))
    r.pump(150, quiet=8)
    print('P2000 output idle after %.1f s' % (time.time() - t0))
    r.show('P2000 after paste', 4, top=True)
    r.send(F2, 5); r.send(F10, 3); r.send(F10, 2)
    p = r.work + '/x.txt'
    print('P2000 file lines:', open(p, 'rb').read().count(b'\n') if os.path.exists(p) else 'no file')
    r.kill()
