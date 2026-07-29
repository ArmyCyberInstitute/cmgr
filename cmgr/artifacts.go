package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (m *Manager) artifactLimits() (int, int64, int64) {
	files := m.policy.MaxArtifactFiles
	totalBytes := m.policy.MaxArtifactBytes
	fileBytes := m.policy.MaxArtifactFileBytes
	if files == 0 {
		files = 10_000
	}
	if totalBytes == 0 {
		totalBytes = 5 * 1024 * 1024 * 1024
	}
	if fileBytes == 0 {
		fileBytes = 1024 * 1024 * 1024
	}
	return files, totalBytes, fileBytes
}

func safeArchiveName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') ||
		strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || path.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive path escapes its root: %q", name)
	}
	if len(clean) > 4096 {
		return "", fmt.Errorf("archive path is too long: %q", name)
	}
	return clean, nil
}

func (m *Manager) cacheArtifacts(
	source io.Reader,
	destination string,
) (files []string, err error) {
	maxFiles, maxBytes, maxFileBytes := m.artifactLimits()
	tempFile, err := os.CreateTemp(m.artifactsDir, ".cmgr-artifacts-*")
	if err != nil {
		return nil, fmt.Errorf("could not create temporary artifact archive: %w", err)
	}
	tempName := tempFile.Name()
	succeeded := false
	closed := false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		if !succeeded {
			_ = os.Remove(tempName)
		}
	}()
	if err := tempFile.Chmod(0600); err != nil {
		return nil, err
	}

	sourceGzip, err := gzip.NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("could not decode artifact gzip stream: %w", err)
	}
	defer sourceGzip.Close()
	sourceTar := tar.NewReader(sourceGzip)
	destinationGzip := gzip.NewWriter(tempFile)
	destinationTar := tar.NewWriter(destinationGzip)

	seen := make(map[string]struct{})
	var totalBytes int64
	entryCount := 0
	for {
		header, nextErr := sourceTar.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("could not read artifact archive: %w", nextErr)
		}
		entryCount++
		if entryCount > maxFiles {
			return nil, fmt.Errorf(
				"artifact archive contains more than %d entries",
				maxFiles,
			)
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("artifact archive repeats path %q", name)
		}
		seen[name] = struct{}{}

		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxFileBytes {
				return nil, fmt.Errorf(
					"artifact %q is %d bytes; per-file maximum is %d",
					name,
					header.Size,
					maxFileBytes,
				)
			}
			if header.Size > maxBytes-totalBytes {
				return nil, fmt.Errorf(
					"artifact archive exceeds %d uncompressed bytes",
					maxBytes,
				)
			}
			totalBytes += header.Size
			files = append(files, name)
		case tar.TypeDir:
			header.Size = 0
		default:
			return nil, fmt.Errorf(
				"artifact %q has unsupported tar type %d",
				name,
				header.Typeflag,
			)
		}

		cleanHeader := *header
		cleanHeader.Name = name
		cleanHeader.Linkname = ""
		cleanHeader.Mode &= 0777
		cleanHeader.Uid = 0
		cleanHeader.Gid = 0
		cleanHeader.Uname = ""
		cleanHeader.Gname = ""
		cleanHeader.PAXRecords = nil
		cleanHeader.Xattrs = nil
		if err := destinationTar.WriteHeader(&cleanHeader); err != nil {
			return nil, fmt.Errorf("could not write artifact header: %w", err)
		}
		if cleanHeader.Typeflag == tar.TypeReg ||
			cleanHeader.Typeflag == tar.TypeRegA {
			if _, err := io.CopyN(destinationTar, sourceTar, cleanHeader.Size); err != nil {
				return nil, fmt.Errorf("could not copy artifact %q: %w", name, err)
			}
		}
	}
	if err := sourceGzip.Close(); err != nil {
		return nil, fmt.Errorf("could not close artifact gzip stream: %w", err)
	}
	if err := destinationTar.Close(); err != nil {
		return nil, fmt.Errorf("could not finish artifact tar stream: %w", err)
	}
	if err := destinationGzip.Close(); err != nil {
		return nil, fmt.Errorf("could not finish artifact gzip stream: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return nil, fmt.Errorf("could not sync artifact archive: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return nil, fmt.Errorf("could not close artifact archive: %w", err)
	}
	closed = true
	if err := os.Rename(tempName, destination); err != nil {
		return nil, fmt.Errorf("could not install artifact archive: %w", err)
	}
	if directory, err := os.Open(filepath.Dir(destination)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	succeeded = true
	return files, nil
}
