package main

import (
	"bufio"
	"os"
	"strings"
)

// IniFile represents a simple parsed INI configuration.
type IniFile struct {
	data map[string]map[string]string
}

// LoadIni reads an INI file into memory. Returns an empty struct if file is missing.
func LoadIni(filename string) *IniFile {
	ini := &IniFile{data: make(map[string]map[string]string)}
	f, err := os.Open(filename)
	if err != nil {
		return ini
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			if ini.data[section] == nil {
				ini.data[section] = make(map[string]string)
			}
		} else if idx := strings.Index(line, "="); idx != -1 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if section != "" {
				ini.data[section][key] = val
			}
		}
	}
	return ini
}

// GetString safely retrieves a value or returns the default.
func (ini *IniFile) GetString(section, key, def string) string {
	if sec, ok := ini.data[section]; ok {
		if val, ok := sec[key]; ok {
			return val
		}
	}
	return def
}