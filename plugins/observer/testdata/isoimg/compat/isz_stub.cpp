// isz_stub.cpp is f4's own no-op backend for isoimg's isz/iszsdk.h interface
// (f4#1563, part 2). The real isz SDK decompresses the ISZ container format
// with zlib/bzip2 and decrypts it with AES (isz/aes.cpp, GPL-2 per the
// f4#1563 recon), neither of which this end-to-end pipeline test needs: it
// proves LoadSubModule/OpenStorage against a plain ISO9660 image, not ISZ.
//
// Every isz_* call here always reports "not an ISZ file" / "no capacity" /
// "no password needed", which sends isoimg's GetImage down its ordinary raw
// ISO9660 path (iso_tc.cpp's GetImage: isz_open returning
// INVALID_HANDLE_VALUE makes image.ImageType stay ISOTYPE_RAW). Real ISZ
// support is out of scope for this part; see the PR description.
//
// This file is f4's own code (same license as the rest of this repository,
// see LICENSE), not a port of isz/aes.cpp or isz/iszsdk.cpp -- it only
// implements the declarations in upstream's isz/iszsdk.h, fetched at build
// time by scripts/build_isoimg_test_wasm.sh, never vendored here.

#include "windows.h"
#include "isz/iszsdk.h"

HANDLE isz_open(HANDLE filePtr, const wchar_t *filespec) {
    (void)filePtr;
    (void)filespec;
    return INVALID_HANDLE_VALUE;
}

int isz_setpassword(HANDLE h_isz, const char *isz_key) {
    (void)h_isz;
    (void)isz_key;
    return 0;
}

unsigned int isz_get_capacity(HANDLE h_isz, unsigned int *sect_size) {
    (void)h_isz;
    if (sect_size) *sect_size = 0;
    return 0;
}

unsigned int isz_read_secs(HANDLE h_isz, void *buffer, unsigned int startsecno,
                            unsigned int sectorcount) {
    (void)h_isz;
    (void)buffer;
    (void)startsecno;
    (void)sectorcount;
    return 0;
}

void isz_close(HANDLE h_isz) { (void)h_isz; }

bool isz_needpassword(HANDLE h_isz) {
    (void)h_isz;
    return false;
}
