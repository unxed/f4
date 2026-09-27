// windows.h is f4's own compat shim standing in for the real Windows headers
// that github.com/lazyhamster/Observer's isoimg module (src/modules/isoimg,
// src/common/ModuleDef.h, src/depends/modulecrt/OptionsParser.cpp) is
// written against, so it builds unmodified with wasi-sdk (f4#1563).
//
// f4 does not vendor isoimg's own LGPL/GPL-licensed sources -- see
// ../../../../../scripts/build_isoimg_test_wasm.sh, which fetches them from
// upstream at a pinned commit at build time -- but this header, and the rest
// of this compat/ directory, are f4's own code (same license as the rest of
// this repository, see LICENSE) and never touch isoimg's own files.
//
// Scope: only what OpenStorage() actually needs to run against a real
// ISO9660 image (f4#1563's part-2 end-to-end test), plus whatever else
// isoimg.cpp/iso_ext.cpp/iso_tc.cpp/OptionsParser.cpp reference so the whole
// module still links -- ExtractItem's write path (CreateFile/WriteFile/
// DeleteFile for the *destination* file) is wired up but never exercised
// here (see doc.go part 1 and the part-2 PR description for what is and
// isn't covered yet).
//
// Two deliberate departures from the real Windows ABI ModuleDef.h targets,
// both matching decisions already made and documented in
// plugins/observer/abi.go and plugins/observer/doc.go for the host side:
//
//   - wchar_t is left at wasi-sdk's native 4-byte wasm32 size, not shrunk to
//     Windows' 2-byte UTF-16 unit with -fshort-wchar. plugins/observer's Go
//     host already reads and writes every wire struct (StorageOpenParams,
//     StorageGeneralInfo, ...) as 4-byte-per-code-point wchar_t for exactly
//     this reason: module and host are compiled/agreed together, so there is
//     no real Windows ABI to match, only the one this pair defines for
//     itself.
//   - MODULE_EXPORT's __stdcall (and CALLBACK) are stripped to nothing
//     rather than kept as an MS calling-convention attribute: wasm32 has one
//     calling convention, and wasi-sdk's clang target does not recognize the
//     MS keyword without -fms-extensions.

#ifndef F4_OBSERVER_COMPAT_WINDOWS_H_
#define F4_OBSERVER_COMPAT_WINDOWS_H_

#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>
#include <wctype.h>
#include <fcntl.h>
#include <unistd.h>
#include <errno.h>
#include <strings.h>

#ifdef __cplusplus
extern "C" {
#endif

// --- calling convention / linkage -----------------------------------------
// wasm32 has no x86 stdcall/cdecl distinction; wasi-sdk's clang does not
// know the MS keywords at all without -fms-extensions, so both are dropped
// to nothing rather than kept as attributes.
#define __stdcall
#define CALLBACK
#define WINAPI

// --- integer / handle types -------------------------------------------------

typedef uint8_t BYTE;
typedef uint16_t WORD;
typedef uint32_t DWORD;
typedef int32_t LONG;
typedef uint32_t ULONG;
typedef unsigned int UINT;
typedef int INT;
typedef int BOOL;
// __int64 is a macro, not a typedef: clang recognizes __int64 specially
// (its MS sized-integer keyword extension) even without -fms-extensions, so
// a plain "typedef long long __int64;" collides with that -- iso.h's own
// "typedef unsigned __int64 Uint64;" only parses as "unsigned long long" if
// __int64 is textually replaced by the preprocessor before the parser ever
// sees it as an identifier.
#define __int64 long long
typedef long long LONGLONG;
typedef unsigned long long ULONGLONG;
typedef wchar_t WCHAR;
typedef char CHAR;
typedef void *LPVOID;
typedef const void *LPCVOID;
typedef void *PVOID;
typedef void *HANDLE;
typedef void *HMODULE;
typedef const wchar_t *LPCWSTR;
typedef wchar_t *LPWSTR;
typedef DWORD *LPDWORD;
typedef const char *LPCSTR;

#ifndef TRUE
#define TRUE 1
#endif
#ifndef FALSE
#define FALSE 0
#endif

#define INVALID_HANDLE_VALUE ((HANDLE)(intptr_t)-1)
#define INVALID_FILE_ATTRIBUTES ((DWORD)-1)
#define MAX_PATH 260

// --- GUID / FILETIME / SYSTEMTIME, packed exactly like the real Windows
// structs, matching plugins/observer/abi.go's GUID/FileTime decoding. -------

#pragma pack(push, 1)

typedef struct {
    DWORD Data1;
    WORD Data2;
    WORD Data3;
    BYTE Data4[8];
} GUID;

typedef struct {
    DWORD dwLowDateTime;
    DWORD dwHighDateTime;
} FILETIME;
typedef FILETIME *LPFILETIME;

typedef struct {
    WORD wYear;
    WORD wMonth;
    WORD wDayOfWeek;
    WORD wDay;
    WORD wHour;
    WORD wMinute;
    WORD wSecond;
    WORD wMilliseconds;
} SYSTEMTIME;
typedef SYSTEMTIME *LPSYSTEMTIME;

// Only .QuadPart is ever read or written by the code this header supports
// (SetFilePointerEx's callers); the LowPart/HighPart view the real Windows
// union also offers is intentionally left out rather than reached for
// through a non-standard anonymous-struct-in-union member.
typedef union {
    LONGLONG QuadPart;
} LARGE_INTEGER;

#pragma pack(pop)

// --- file access -------------------------------------------------------

#define GENERIC_READ 0x80000000u
#define GENERIC_WRITE 0x40000000u
#define FILE_SHARE_READ 0x00000001u
#define FILE_SHARE_WRITE 0x00000002u
#define OPEN_EXISTING 3u
#define CREATE_ALWAYS 2u

#define FILE_BEGIN 0u
#define FILE_CURRENT 1u
#define FILE_END 2u

#define FILE_ATTRIBUTE_NORMAL 0x00000080u
#define FILE_ATTRIBUTE_HIDDEN 0x00000002u
#define FILE_ATTRIBUTE_DIRECTORY 0x00000010u
#define FILE_ATTRIBUTE_ARCHIVE 0x00000020u
#define FILE_ATTRIBUTE_SYSTEM 0x00000004u

#define CP_ACP 0u
#define CP_UTF8 65001u

// f4_wide_to_utf8 / f4_utf8_len_of_wide are internal helpers, not part of
// any Windows API, used by CreateFile/GetFileAttributes/DeleteFile below to
// turn the wchar_t* path a module builds (see plugins/observer/wchar.go's
// f4observer_open_storage-side counterpart) into the byte path wasi's
// path_open actually wants. They only have to round-trip whatever
// plugins/observer's SingleFileFS mount already exposes: the guest-visible
// ASCII path f4 chose (fsbridge.go), never a real Windows path.
static inline int f4_wide_to_utf8(const wchar_t *w, char *out, size_t outCap) {
    size_t o = 0;
    for (; *w; w++) {
        uint32_t c = (uint32_t)*w;
        if (c < 0x80) {
            if (o + 1 >= outCap) return -1;
            out[o++] = (char)c;
        } else if (c < 0x800) {
            if (o + 2 >= outCap) return -1;
            out[o++] = (char)(0xC0 | (c >> 6));
            out[o++] = (char)(0x80 | (c & 0x3F));
        } else if (c < 0x10000) {
            if (o + 3 >= outCap) return -1;
            out[o++] = (char)(0xE0 | (c >> 12));
            out[o++] = (char)(0x80 | ((c >> 6) & 0x3F));
            out[o++] = (char)(0x80 | (c & 0x3F));
        } else {
            if (o + 4 >= outCap) return -1;
            out[o++] = (char)(0xF0 | (c >> 18));
            out[o++] = (char)(0x80 | ((c >> 12) & 0x3F));
            out[o++] = (char)(0x80 | ((c >> 6) & 0x3F));
            out[o++] = (char)(0x80 | (c & 0x3F));
        }
    }
    out[o] = 0;
    return (int)o;
}

static inline HANDLE CreateFile(const wchar_t *path, DWORD access, DWORD share,
                                 void *sa, DWORD disposition, DWORD flags,
                                 HANDLE hTemplate) {
    (void)share;
    (void)sa;
    (void)flags;
    (void)hTemplate;
    char narrow[4096];
    if (!path || f4_wide_to_utf8(path, narrow, sizeof(narrow)) < 0) {
        return INVALID_HANDLE_VALUE;
    }

    int oflags;
    if (access & GENERIC_WRITE) {
        oflags = O_WRONLY;
        if (disposition == CREATE_ALWAYS) {
            oflags |= O_CREAT | O_TRUNC;
        } else {
            oflags |= O_CREAT;
        }
    } else {
        oflags = O_RDONLY;
    }

    int fd = open(narrow, oflags, 0644);
    if (fd < 0) {
        return INVALID_HANDLE_VALUE;
    }
    // fd 0 is a valid descriptor but this module's own code treats a zero
    // HANDLE as "absent" (see IsoImage.hFile checks); shift by one so a
    // successful open() of fd 0 never round-trips as a null/invalid handle.
    return (HANDLE)(intptr_t)(fd + 1);
}

static inline int f4_handle_fd(HANDLE h) {
    intptr_t v = (intptr_t)h;
    if (v <= 0) return -1;
    return (int)(v - 1);
}

static inline BOOL ReadFile(HANDLE h, void *buf, DWORD toRead,
                             DWORD *outRead, void *overlapped) {
    (void)overlapped;
    int fd = f4_handle_fd(h);
    if (fd < 0) {
        if (outRead) *outRead = 0;
        return FALSE;
    }
    ssize_t n = read(fd, buf, toRead);
    if (n < 0) {
        if (outRead) *outRead = 0;
        return FALSE;
    }
    if (outRead) *outRead = (DWORD)n;
    return TRUE;
}

static inline BOOL WriteFile(HANDLE h, const void *buf, DWORD toWrite,
                              DWORD *written, void *overlapped) {
    (void)overlapped;
    int fd = f4_handle_fd(h);
    if (fd < 0) {
        if (written) *written = 0;
        return FALSE;
    }
    ssize_t n = write(fd, buf, toWrite);
    if (n < 0) {
        if (written) *written = 0;
        return FALSE;
    }
    if (written) *written = (DWORD)n;
    return TRUE;
}

static inline BOOL CloseHandle(HANDLE h) {
    int fd = f4_handle_fd(h);
    if (fd < 0) return FALSE;
    return close(fd) == 0 ? TRUE : FALSE;
}

static inline DWORD SetFilePointer(HANDLE h, LONG distanceLow,
                                    LONG *distanceHighPtr, DWORD method) {
    int fd = f4_handle_fd(h);
    if (fd < 0) return (DWORD)-1;
    int64_t distance = (int64_t)distanceLow;
    if (distanceHighPtr) {
        distance |= ((int64_t)(*distanceHighPtr)) << 32;
    }
    int whence = (method == FILE_CURRENT) ? SEEK_CUR
                 : (method == FILE_END)   ? SEEK_END
                                          : SEEK_SET;
    off_t pos = lseek(fd, (off_t)distance, whence);
    if (pos < 0) return (DWORD)-1;
    if (distanceHighPtr) *distanceHighPtr = (LONG)((uint64_t)pos >> 32);
    return (DWORD)(uint64_t)pos;
}

static inline BOOL SetFilePointerEx(HANDLE h, LARGE_INTEGER distance,
                                     LARGE_INTEGER *newPos, DWORD method) {
    int fd = f4_handle_fd(h);
    if (fd < 0) return FALSE;
    int whence = (method == FILE_CURRENT) ? SEEK_CUR
                 : (method == FILE_END)   ? SEEK_END
                                          : SEEK_SET;
    off_t pos = lseek(fd, (off_t)distance.QuadPart, whence);
    if (pos < 0) return FALSE;
    if (newPos) newPos->QuadPart = (LONGLONG)pos;
    return TRUE;
}

static inline DWORD GetFileAttributes(const wchar_t *path) {
    char narrow[4096];
    if (!path || f4_wide_to_utf8(path, narrow, sizeof(narrow)) < 0) {
        return INVALID_FILE_ATTRIBUTES;
    }
    if (access(narrow, F_OK) != 0) {
        return INVALID_FILE_ATTRIBUTES;
    }
    return FILE_ATTRIBUTE_NORMAL;
}

static inline BOOL DeleteFile(const wchar_t *path) {
    char narrow[4096];
    if (!path || f4_wide_to_utf8(path, narrow, sizeof(narrow)) < 0) {
        return FALSE;
    }
    return unlink(narrow) == 0 ? TRUE : FALSE;
}

// --- ZeroMemory / CopyMemory --------------------------------------------

#define ZeroMemory(dst, size) memset((dst), 0, (size))
#define CopyMemory(dst, src, size) memcpy((dst), (src), (size))

// min/max: real WinDef.h defines these function-like macros unless the
// including code defines NOMINMAX first, which isoimg/iso_ext/iso_tc do not.
#ifndef max
#define max(a, b) (((a) > (b)) ? (a) : (b))
#endif
#ifndef min
#define min(a, b) (((a) < (b)) ? (a) : (b))
#endif

// _strnicmp: MSVC CRT name for a case-insensitive, length-bounded compare;
// wasi-sdk's libc has the POSIX name instead.
#define _strnicmp strncasecmp

// --- codepage conversion --------------------------------------------------
// GetACP: the real call returns the process's configured ANSI codepage.
// isoimg only uses the result to pick a decode path (UTF-8 vs. "whatever
// 8-bit codepage this disc claims"); returning a fixed, clearly-non-UTF-8
// value here is enough to exercise both branches without pulling in a real
// codepage table for this end-to-end test.
static inline UINT GetACP(void) { return 1252u; }

// MultiByteToWideChar: CP_UTF8 gets a real UTF-8 decode (isoimg's Joliet
// path uses it); every other codepage -- including CP_ACP as returned by
// GetACP() above -- widens byte-for-byte, which is exact for ASCII and good
// enough for this test's synthetic, ASCII-only volume descriptors.
static inline int MultiByteToWideChar(UINT codepage, DWORD flags,
                                       const char *src, int srcLen,
                                       wchar_t *dst, int dstCap) {
    (void)flags;
    if (srcLen < 0) srcLen = (int)strlen(src);
    int produced = 0;
    if (codepage == CP_UTF8) {
        int i = 0;
        while (i < srcLen) {
            unsigned char c0 = (unsigned char)src[i];
            uint32_t cp;
            int len;
            if (c0 < 0x80) {
                cp = c0;
                len = 1;
            } else if ((c0 & 0xE0) == 0xC0 && i + 1 < srcLen) {
                cp = ((uint32_t)(c0 & 0x1F) << 6) | (src[i + 1] & 0x3F);
                len = 2;
            } else if ((c0 & 0xF0) == 0xE0 && i + 2 < srcLen) {
                cp = ((uint32_t)(c0 & 0x0F) << 12) |
                     ((uint32_t)(src[i + 1] & 0x3F) << 6) |
                     (src[i + 2] & 0x3F);
                len = 3;
            } else if ((c0 & 0xF8) == 0xF0 && i + 3 < srcLen) {
                cp = ((uint32_t)(c0 & 0x07) << 18) |
                     ((uint32_t)(src[i + 1] & 0x3F) << 12) |
                     ((uint32_t)(src[i + 2] & 0x3F) << 6) |
                     (src[i + 3] & 0x3F);
                len = 4;
            } else {
                cp = 0xFFFD;
                len = 1;
            }
            if (dst) {
                if (produced >= dstCap) break;
                dst[produced] = (wchar_t)cp;
            }
            produced++;
            i += len;
        }
    } else {
        for (int i = 0; i < srcLen; i++) {
            if (dst) {
                if (produced >= dstCap) break;
                dst[produced] = (wchar_t)(unsigned char)src[i];
            }
            produced++;
        }
    }
    return produced;
}

// --- SYSTEMTIME <-> FILETIME ---------------------------------------------
// Real, not stubbed: OpenStorage's returned StorageGeneralInfo.Created goes
// through this, and the part-2 end-to-end test checks it comes back
// non-garbage. Same epoch/scale math as ModuleCRT.h's UnixTimeToFileTime
// (100ns ticks since 1601-01-01), computed here from a SYSTEMTIME's fields
// with a small fixed days-since-epoch table rather than depending on any
// libc struct tm / timegm behavior around the 1601 epoch.
static inline int f4_is_leap_year(int y) {
    return (y % 4 == 0 && y % 100 != 0) || (y % 400 == 0);
}

static inline void SystemTimeToFileTime(const SYSTEMTIME *st, FILETIME *ft) {
    static const int cumDays[12] = {0,   31,  59,  90,  120, 151,
                                     181, 212, 243, 273, 304, 334};
    // Days from 1601-01-01 to st's year-01-01.
    long long days = 0;
    for (int y = 1601; y < st->wYear; y++) {
        days += f4_is_leap_year(y) ? 366 : 365;
    }
    days += cumDays[(st->wMonth >= 1 && st->wMonth <= 12) ? st->wMonth - 1 : 0];
    if (st->wMonth > 2 && f4_is_leap_year(st->wYear)) days += 1;
    days += (st->wDay > 0 ? st->wDay - 1 : 0);

    long long seconds = days * 86400LL + st->wHour * 3600LL +
                         st->wMinute * 60LL + st->wSecond;
    long long ticks = seconds * 10000000LL + (long long)st->wMilliseconds * 10000LL;

    ft->dwLowDateTime = (DWORD)(uint64_t)ticks;
    ft->dwHighDateTime = (DWORD)((uint64_t)ticks >> 32);
}

// --- wide-string helpers ---------------------------------------------------
// wasi-sdk's libc already implements the ISO C wide-character functions
// (wcslen, wcscpy, wcscat, wcsncpy, wcscmp, wcschr, wcsrchr, wcstol) against
// its native 4-byte wchar_t, so only the MSVC-only names isoimg/OptionsParser
// call are added here, each a thin wrapper over the standard one.

typedef int errno_t;

static inline errno_t wcscpy_s_impl(wchar_t *dst, size_t dstCap,
                                     const wchar_t *src) {
    size_t n = wcslen(src);
    if (n + 1 > dstCap) {
        if (dstCap) dst[0] = 0;
        return ERANGE;
    }
    memcpy(dst, src, (n + 1) * sizeof(wchar_t));
    return 0;
}
#define wcscpy_s(dst, dstCap, src) wcscpy_s_impl((dst), (dstCap), (src))

static inline errno_t wcscat_s_impl(wchar_t *dst, size_t dstCap,
                                     const wchar_t *src) {
    size_t dn = wcslen(dst);
    size_t sn = wcslen(src);
    if (dn + sn + 1 > dstCap) {
        return ERANGE;
    }
    memcpy(dst + dn, src, (sn + 1) * sizeof(wchar_t));
    return 0;
}
#define wcscat_s(dst, dstCap, src) wcscat_s_impl((dst), (dstCap), (src))

static inline wchar_t *lstrcpy(wchar_t *dst, const wchar_t *src) {
    wcscpy(dst, src);
    return dst;
}

static inline wchar_t *lstrcat(wchar_t *dst, const wchar_t *src) {
    wcscat(dst, src);
    return dst;
}

// Windows lstrcpyn always NUL-terminates within maxLen wchar_ts (including
// the terminator), unlike wcsncpy.
static inline wchar_t *lstrcpyn(wchar_t *dst, const wchar_t *src, int maxLen) {
    if (maxLen <= 0) return dst;
    int i = 0;
    for (; i < maxLen - 1 && src[i]; i++) dst[i] = src[i];
    dst[i] = 0;
    return dst;
}

static inline int lstrlen(const wchar_t *s) { return (int)wcslen(s); }

static inline int _wcsicmp(const wchar_t *a, const wchar_t *b) {
    for (;;) {
        wchar_t ca = (wchar_t)towlower((wint_t)*a);
        wchar_t cb = (wchar_t)towlower((wint_t)*b);
        if (ca != cb) return (ca < cb) ? -1 : 1;
        if (!ca) return 0;
        a++;
        b++;
    }
}

static inline wchar_t *_wcsdup(const wchar_t *s) {
    size_t n = (wcslen(s) + 1) * sizeof(wchar_t);
    wchar_t *p = (wchar_t *)malloc(n);
    if (p) memcpy(p, s, n);
    return p;
}

#ifdef __cplusplus
}
#endif

#endif // F4_OBSERVER_COMPAT_WINDOWS_H_
