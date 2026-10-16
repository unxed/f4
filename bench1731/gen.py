#!/usr/bin/env python3
"""Temporary data generator for unxed/f4#1731 (measurement stand, tmp branch only).

Builds a mixed tree: ELF binaries, text, already-compressed data and random
bytes, from files that exist on a GitHub runner. Deterministic order, byte
budgets per class.
"""
import os
import re
import shutil
import subprocess
import sys

root = sys.argv[1]
MB = 1024 * 1024
bad = re.compile(r"[^A-Za-z0-9._+,@=~/-]")

stats = {}


def is_elf(path):
    try:
        with open(path, "rb") as f:
            return f.read(4) == b"\x7fELF"
    except OSError:
        return False


def take(label, srcs, budget, maxfile, want_elf, name_ok=None):
    dst_total = 0
    count = 0
    for src in srcs:
        if not os.path.isdir(src):
            continue
        for dirpath, dirnames, filenames in os.walk(src):
            dirnames.sort()
            for fn in sorted(filenames):
                p = os.path.join(dirpath, fn)
                if dst_total >= budget:
                    break
                rel = os.path.relpath(p, "/")
                if bad.search(rel):
                    continue
                if os.path.islink(p) or not os.path.isfile(p):
                    continue
                if name_ok is not None and not name_ok(fn):
                    continue
                try:
                    size = os.path.getsize(p)
                except OSError:
                    continue
                if size == 0 or size > maxfile:
                    continue
                elf = is_elf(p)
                if elf != want_elf:
                    continue
                out = os.path.join(root, label, rel)
                try:
                    os.makedirs(os.path.dirname(out), exist_ok=True)
                    shutil.copyfile(p, out)
                except OSError:
                    continue
                dst_total += size
                count += 1
            if dst_total >= budget:
                break
        if dst_total >= budget:
            break
    stats[label] = (count, dst_total)
    print("gen %-12s files=%5d bytes=%6.1f MB" % (label, count, dst_total / MB), flush=True)


goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True).stdout.strip()
cache = "/opt/hostedtoolcache"

take("elf-bin", ["/usr/bin", "/usr/sbin", "/usr/local/bin"], 170 * MB, 40 * MB, True)
take("elf-lib", ["/usr/lib/x86_64-linux-gnu", "/usr/lib"], 90 * MB, 20 * MB, True)
take("elf-go", [goroot + "/pkg/tool", goroot + "/bin", cache], 50 * MB, 30 * MB, True)
txt = lambda fn: not re.search(r"\.(gz|xz|bz2|zip|png|jpg|jpeg|zst|woff2?|jar|whl|a|o)$", fn)
take("text-go", [goroot + "/src"], 110 * MB, 4 * MB, False, txt)
take("text-usr", ["/usr/share/doc", "/usr/include", "/usr/share/perl", "/usr/lib/python3"], 60 * MB, 4 * MB, False, txt)
comp = lambda fn: re.search(r"\.(gz|xz|bz2|zip|png|jpg|jpeg|zst|woff2?|jar|whl)$", fn) is not None
take("packed", ["/usr/share", goroot + "/src", goroot + "/test", cache], 40 * MB, 8 * MB, False, comp)

os.makedirs(os.path.join(root, "random"), exist_ok=True)
for i in range(8):
    with open(os.path.join(root, "random", "rnd%02d.bin" % i), "wb") as f:
        f.write(os.urandom(3 * MB))
print("gen random       files=    8 bytes=  24.0 MB")

total = sum(b for _, b in stats.values()) + 24 * MB
files = sum(c for c, _ in stats.values()) + 8
print("gen TOTAL files=%d bytes=%.1f MB" % (files, total / MB))
