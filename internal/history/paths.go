package history

import (
	"encoding/json"

	"github.com/unxed/vtui"
)

const CommandHistoryPathsID = "cmdline.paths"

type commandHistoryPathRecord struct {
	Command string `json:"command"`
	Path    string `json:"path"`
}

func LoadCommandHistoryPaths(commands []string) []string {
	paths := make([]string, len(commands))
	if len(commands) == 0 {
		return paths
	}
	byCommand := legacyCommandPaths(vtui.GlobalHistoryProvider)
	for i, command := range commands {
		paths[i] = byCommand[command]
	}
	return paths
}

func SaveCommandHistoryPaths(commands, paths []string) {
	if vtui.GlobalHistoryProvider == nil {
		return
	}

	encoded := make([]string, 0, len(commands))
	for i, command := range commands {
		if command == "" || i >= len(paths) || paths[i] == "" {
			continue
		}
		data, err := json.Marshal(commandHistoryPathRecord{Command: command, Path: paths[i]})
		if err == nil {
			encoded = append(encoded, string(data))
		}
	}
	vtui.GlobalHistoryProvider.SaveHistory(CommandHistoryPathsID, encoded)
}

func RememberCommandHistoryPath(command, path string, commands []string) {
	if command == "" || path == "" || len(commands) == 0 {
		return
	}
	paths := LoadCommandHistoryPaths(commands)
	for i, candidate := range commands {
		if candidate == command {
			paths[i] = path
			break
		}
	}
	SaveCommandHistoryPaths(commands, paths)
}
