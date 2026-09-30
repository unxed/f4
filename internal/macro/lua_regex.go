//go:build !extralite

package macro

// regex.new for Far 3's macros (f4#1686, step 7 corpus): the pattern of Far's
// regular-expression library turned into Go's regexp, and the few methods
// scripts call. Far's engine is PCRE-like; what Go's RE2 cannot express
// (look-behind, back-references) makes regex.new return an object that never
// matches, and says so in the log, so a file that builds its patterns at load
// time still loads.

import (
	"regexp"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// farRegexClasses are Far's letter classes, spelled in Go's syntax.
var farRegexClasses = strings.NewReplacer(
	`\i`, `[\p{L}_]`, // identifier start
	`\I`, `[^\p{L}_]`,
	`\l`, `\p{Ll}`, // lower case
	`\L`, `\P{Ll}`,
	`\u`, `\p{Lu}`, // upper case
	`\U`, `\P{Lu}`,
)

// translateFarRegex turns a Far pattern and its flags ("i" case, "x" extended,
// "s" dot matches newline, "m" multi-line) into Go's syntax.
func translateFarRegex(pattern, flags string) string {
	if strings.Contains(flags, "x") {
		pattern = stripExtended(pattern)
	}
	pattern = farRegexClasses.Replace(pattern)
	prefix := ""
	for _, f := range flags {
		switch f {
		case 'i', 's', 'm':
			prefix += string(f)
		}
	}
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	return pattern
}

// stripExtended removes the white space and #-comments an extended pattern
// carries, except inside a character class or after a backslash.
func stripExtended(p string) string {
	var out strings.Builder
	inClass, escaped, comment := false, false, false
	for _, r := range p {
		switch {
		case comment:
			if r == '\n' {
				comment = false
			}
		case escaped:
			out.WriteRune(r)
			escaped = false
		case r == '\\':
			out.WriteRune(r)
			escaped = true
		case inClass:
			out.WriteRune(r)
			if r == ']' {
				inClass = false
			}
		case r == '[':
			out.WriteRune(r)
			inClass = true
		case r == '#':
			comment = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// luaRegexNew is regex.new(pattern [, flags]).
func (e *LuaMacroEngine) luaRegexNew(L *lua.LState) int {
	pattern := L.CheckString(1)
	flags := ""
	if L.GetTop() >= 2 {
		flags = L.CheckString(2)
	}
	re, err := regexp.Compile(translateFarRegex(pattern, flags))
	if err != nil {
		e.host.Log("MACRO: regex.new: %v (the pattern never matches)", err)
		re = nil
	}

	captures := func(L *lua.LState, s string, m []int) int {
		// The whole match when the pattern has no groups, else the groups.
		if len(m) <= 2 {
			L.Push(lua.LString(s[m[0]:m[1]]))
			return 1
		}
		n := 0
		for i := 2; i+1 < len(m); i += 2 {
			if m[i] < 0 {
				L.Push(lua.LFalse)
			} else {
				L.Push(lua.LString(s[m[i]:m[i+1]]))
			}
			n++
		}
		return n
	}

	object := L.NewTable()
	L.SetFuncs(object, map[string]lua.LGFunction{
		// r:match(s [, init]) - the captures (or the whole match), nil if none.
		"match": func(L *lua.LState) int {
			s := L.CheckString(2)
			if re == nil {
				L.Push(lua.LNil)
				return 1
			}
			start := 0
			if L.GetTop() >= 3 {
				if init := L.CheckInt(3); init > 1 && init-1 <= len(s) {
					start = init - 1
				}
			}
			m := re.FindStringSubmatchIndex(s[start:])
			if m == nil {
				L.Push(lua.LNil)
				return 1
			}
			for i := range m {
				if m[i] >= 0 {
					m[i] += start
				}
			}
			return captures(L, s, m)
		},
		// r:find(s [, init]) - first, last (1-based, inclusive) and the captures.
		"find": func(L *lua.LState) int {
			s := L.CheckString(2)
			if re == nil {
				L.Push(lua.LNil)
				return 1
			}
			start := 0
			if L.GetTop() >= 3 {
				if init := L.CheckInt(3); init > 1 && init-1 <= len(s) {
					start = init - 1
				}
			}
			m := re.FindStringSubmatchIndex(s[start:])
			if m == nil {
				L.Push(lua.LNil)
				return 1
			}
			for i := range m {
				if m[i] >= 0 {
					m[i] += start
				}
			}
			L.Push(lua.LNumber(m[0] + 1))
			L.Push(lua.LNumber(m[1]))
			n := 2
			if len(m) > 2 {
				n += captures(L, s, m)
			}
			return n
		},
		// r:gsub(s, repl) - repl is a string ($1 for a group) or a function of the captures.
		"gsub": func(L *lua.LState) int {
			s := L.CheckString(2)
			if re == nil {
				L.Push(lua.LString(s))
				L.Push(lua.LNumber(0))
				return 2
			}
			count := 0
			var out strings.Builder
			last := 0
			for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
				count++
				out.WriteString(s[last:m[0]])
				switch repl := L.Get(3).(type) {
				case *lua.LFunction:
					L.Push(repl)
					n := captures(L, s, m)
					// captures pushed above the function: call with them.
					L.Call(n, 1)
					out.WriteString(lua.LVAsString(L.Get(-1)))
					L.Pop(1)
				default:
					var dst []byte
					dst = re.ExpandString(dst, strings.ReplaceAll(lua.LVAsString(repl), "%", "$"), s, m)
					out.Write(dst)
				}
				last = m[1]
			}
			out.WriteString(s[last:])
			L.Push(lua.LString(out.String()))
			L.Push(lua.LNumber(count))
			return 2
		},
	})
	// r:gmatch(s) - an iterator over the matches, each as match would answer.
	L.SetField(object, "gmatch", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(2)
		var all [][]int
		if re != nil {
			all = re.FindAllStringSubmatchIndex(s, -1)
		}
		i := 0
		L.Push(L.NewFunction(func(L *lua.LState) int {
			if i >= len(all) {
				L.Push(lua.LNil)
				return 1
			}
			m := all[i]
			i++
			return captures(L, s, m)
		}))
		return 1
	}))
	// The methods are called as r:match(s), so the object is the first argument.
	L.Push(object)
	meta := L.NewTable()
	meta.RawSetString("__index", object)
	L.SetMetatable(object, meta)
	return 1
}

func (e *LuaMacroEngine) installRegex(L *lua.LState) {
	ns := L.NewTable()
	L.SetField(ns, "new", L.NewFunction(e.luaRegexNew))
	L.SetGlobal("regex", ns)
}
