package envman

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/unxed/f4/vfs"
)

const maxEnvironmentFileSize = 16 << 20

func resolveVFSPath(filesystem vfs.VFS, base, raw string) (string, error) {
	if filesystem == nil {
		return "", errors.New("active panel has no file system")
	}
	value := strings.TrimSpace(raw)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			value = value[1 : len(value)-1]
		}
	}
	if value == "" {
		return "", errors.New("file name is empty")
	}
	if !filesystem.IsAbs(value) {
		if base == "" {
			base = filesystem.GetPath()
		}
		value = filesystem.Join(base, value)
	}
	absolute, err := filesystem.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve VFS path: %w", err)
	}
	return absolute, nil
}

func readVFSFile(ctx context.Context, filesystem vfs.VFS, path string, limit int64) ([]byte, error) {
	if filesystem == nil {
		return nil, errors.New("file system is unavailable")
	}
	if limit <= 0 {
		limit = maxEnvironmentFileSize
	}
	reader, err := filesystem.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if size := reader.Size(); size > limit {
		return nil, fmt.Errorf("environment file is too large (%d bytes; limit %d)", size, limit)
	}

	var output bytes.Buffer
	if size := reader.Size(); size > 0 && size <= limit {
		output.Grow(int(size))
	}
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(ctx, buffer)
		if n > 0 {
			if int64(output.Len()+n) > limit {
				return nil, fmt.Errorf("environment file exceeds %d bytes", limit)
			}
			_, _ = output.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	return output.Bytes(), nil
}

func writeVFSFile(ctx context.Context, filesystem vfs.VFS, path string, data []byte) (err error) {
	if filesystem == nil {
		return errors.New("file system is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	writer, err := filesystem.Create(ctx, path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, writeErr := writer.Write(data[offset:])
		if writeErr != nil {
			return writeErr
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		offset += n
	}
	return nil
}
