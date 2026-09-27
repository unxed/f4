// StdAfx.h stands in for isoimg's own precompiled-header file
// (src/modules/isoimg/stdafx.h in github.com/lazyhamster/Observer) for this
// build (f4#1563, part 2).
//
// Every isoimg/OptionsParser source file quote-includes "StdAfx.h" (capital
// S), not "stdafx.h": on the real, case-insensitive Windows filesystem this
// resolves to isoimg's own stdafx.h, but on the case-sensitive filesystem
// wasi-sdk's clang runs on it never matches that file at all. Rather than
// renaming upstream's own file (which f4 does not vendor -- see
// ../../../../../scripts/build_isoimg_test_wasm.sh), this compat directory
// is put ahead of isoimg's own directory on the include path, so every
// "StdAfx.h" quote-include resolves here instead.
//
// f4's own code (same license as the rest of this repository, see LICENSE).

#ifndef F4_OBSERVER_COMPAT_STDAFX_H_
#define F4_OBSERVER_COMPAT_STDAFX_H_

#include "windows.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>

// The real stdafx.h only defines DebugString's OutputDebugStringA-calling
// form under DEBUG/_DEBUG, which this build never defines; unconditionally
// empty is the same behavior this build would get either way.
#define DebugString(x)

#endif // F4_OBSERVER_COMPAT_STDAFX_H_
