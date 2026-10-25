package updates

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Set by the build scripts; dev builds compare against their nearest release tag.
var Version = "dev"
var BaseVersion = ""

const maxUnpackedSize = 512 << 20

// Stage verifies the archive and extracts only public program files, never user data.
func Stage(ctx context.Context, client *http.Client, release *Release, executable string, progress func(int64, int64)) (_ *Pending, err error) {
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	installRoot, err := installationRoot(executable)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(filepath.Dir(installRoot), ".fdu-update-")
	if err != nil {
		return nil, fmt.Errorf("无法创建更新目录，请检查安装目录的写入权限: %w", err)
	}
	defer func() {
		if err != nil {
			// root is the exact directory just returned by MkdirTemp, never an archive path.
			_ = os.RemoveAll(root)
		}
	}()
	archive := filepath.Join(root, release.AssetName)
	if err = Download(ctx, client, release, archive, progress); err != nil {
		return nil, err
	}
	payload := filepath.Join(root, "payload")
	if err = os.Mkdir(payload, 0o700); err != nil {
		return nil, err
	}
	pending := &Pending{Root: root, Executable: executable, Version: release.Version}
	seen := make(map[string]bool)
	var total int64
	entries := 0
	extract := func(name string, mode os.FileMode, size int64, reader io.Reader) error {
		entries++
		if entries > 10000 || size < 0 || size > maxUnpackedSize-total {
			return errors.New("更新包解压大小超出限制")
		}
		total += size
		if err := ctx.Err(); err != nil {
			return err
		}
		clean := path.Clean(strings.TrimSuffix(name, "/"))
		if clean == "." || clean != strings.TrimSuffix(name, "/") || strings.ContainsAny(name, "\\:\x00") || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("更新包包含不安全的路径")
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return errors.New("更新包包含链接或特殊文件")
		}
		var relative string
		if runtime.GOOS == "darwin" {
			if clean != "FDU Connect.app" && !strings.HasPrefix(clean, "FDU Connect.app/") {
				return nil
			}
			relative = clean
		} else {
			relative = clean
			if runtime.GOOS == "linux" {
				relative = strings.TrimPrefix(clean, "FDU-Connect/")
			}
			if mode.IsDir() || !publicFile(relative) {
				return nil
			}
		}
		key := strings.ToLower(relative)
		if seen[key] {
			return errors.New("更新包包含重复文件")
		}
		seen[key] = true
		target := filepath.Join(payload, filepath.FromSlash(relative))
		if mode.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		permissions := os.FileMode(0o644)
		if mode&0o111 != 0 || path.Base(relative) == "fdu-connect-gui" || path.Base(relative) == "fdu-connect" || path.Base(relative) == "FDU Connect" {
			permissions = 0o755
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(f, io.LimitReader(reader, size+1))
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != size {
			return errors.New("更新包文件大小不匹配")
		}
		return nil
	}
	if strings.HasSuffix(release.AssetName, ".zip") {
		z, openErr := zip.OpenReader(archive)
		if openErr != nil {
			return nil, openErr
		}
		for _, file := range z.File {
			reader, openErr := file.Open()
			if openErr != nil {
				err = openErr
				break
			}
			err = extract(file.Name, file.Mode(), int64(file.UncompressedSize64), reader)
			_ = reader.Close()
			if err != nil {
				break
			}
		}
		_ = z.Close()
		if err != nil {
			return nil, err
		}
	} else {
		f, openErr := os.Open(archive)
		if openErr != nil {
			return nil, openErr
		}
		defer f.Close()
		gz, openErr := gzip.NewReader(f)
		if openErr != nil {
			return nil, openErr
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, nextErr := tr.Next()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return nil, nextErr
			}
			if err = extract(h.Name, h.FileInfo().Mode(), h.Size, tr); err != nil {
				return nil, err
			}
		}
	}
	if runtime.GOOS == "darwin" {
		bundle := filepath.Join(payload, "FDU Connect.app")
		for _, name := range []string{"FDU Connect", "fdu-connect"} {
			info, statErr := os.Stat(filepath.Join(bundle, "Contents", "MacOS", name))
			if statErr != nil || !info.Mode().IsRegular() {
				return nil, errors.New("更新包缺少 GUI 或核心程序")
			}
		}
		hash, hashErr := Digest(bundle)
		if hashErr != nil {
			return nil, hashErr
		}
		pending.Files = []File{{Source: bundle, Target: installRoot, SHA256: hash}}
	} else {
		files, readErr := os.ReadDir(payload)
		if readErr != nil {
			return nil, readErr
		}
		for _, file := range files {
			source := filepath.Join(payload, file.Name())
			hash, hashErr := Digest(source)
			if hashErr != nil {
				return nil, hashErr
			}
			pending.Files = append(pending.Files, File{Source: source, Target: filepath.Join(installRoot, file.Name()), SHA256: hash})
		}
	}
	if err = Validate(pending); err != nil {
		return nil, err
	}
	return pending, nil
}

func publicFile(name string) bool {
	switch name {
	case "config.toml.example", "LICENSE":
		return true
	case "FDU-Connect-GUI.exe", "fdu-connect.exe":
		return runtime.GOOS == "windows"
	case "fdu-connect-gui", "fdu-connect", "fdu-connect.desktop", "fdu-connect.png":
		return runtime.GOOS == "linux"
	}
	return false
}
