// The native launcher selects a writable temporary directory before NSIS starts.
package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	self, err := os.Executable()
	code := 1
	if err == nil {
		code, err = runSetup(self, commandTail(rawCommandLine()), tempCandidates(self))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if !silent() {
			showError("无法准备安装所需的临时文件。请检查磁盘空间和目录访问权限。\r\n\r\n" + err.Error())
		}
		code = 1
	}
	os.Exit(code)
}

func runSetup(archive, tail string, bases []string) (int, error) {
	dir, executable, err := prepareSetup(archive, bases)
	if err != nil {
		return 1, err
	}
	defer func() {
		if err := removeWorkDir(dir); err != nil {
			fmt.Fprintln(os.Stderr, "清理安装临时文件失败:", err)
		}
	}()
	cmd := exec.Command(executable)
	// NSIS requires the final /D=path argument to stay unquoted, even with spaces.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(executable) + " " + tail}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(name, "TEMP") && !strings.EqualFold(name, "TMP") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "TEMP="+dir, "TMP="+dir)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func prepareSetup(archive string, bases []string) (string, string, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return "", "", fmt.Errorf("安装包内容无效: %w", err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "setup.exe" || z.File[0].UncompressedSize64 == 0 {
		return "", "", errors.New("安装包内容无效")
	}
	var failures error
	seen := make(map[string]bool)
	for _, base := range bases {
		key := strings.ToLower(filepath.Clean(base))
		if base == "" || seen[key] {
			continue
		}
		seen[key] = true
		dir, executable, err := extractSetup(z.File[0], base)
		if err == nil {
			return dir, executable, nil
		}
		failures = errors.Join(failures, fmt.Errorf("%s: %w", base, err))
	}
	if failures == nil {
		failures = errors.New("没有可用的临时目录")
	}
	return "", "", failures
}

func extractSetup(payload *zip.File, base string) (dir, executable string, err error) {
	if !filepath.IsAbs(base) {
		return "", "", errors.New("临时目录必须是完整路径")
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return "", "", err
	}
	dir, err = os.MkdirTemp(base, "fdu-setup-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = removeWorkDir(dir)
		}
	}()
	// Leave room for NSIS's own plugin directory and filenames under MAX_PATH.
	if len(syscall.StringToUTF16(dir))+50 >= 260 {
		return dir, "", errors.New("临时目录路径过长")
	}
	if err = probeTemp(dir); err != nil {
		return dir, "", err
	}
	executable = filepath.Join(dir, "setup.exe")
	reader, err := payload.Open()
	if err != nil {
		return dir, "", err
	}
	defer reader.Close()
	f, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return dir, "", err
	}
	_, copyErr := io.Copy(f, reader) // ZIP validates the payload CRC before execution.
	err = errors.Join(copyErr, f.Close())
	return dir, executable, err
}

func removeWorkDir(dir string) error {
	if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Base(dir), "fdu-setup-") {
		return errors.New("拒绝清理非安装临时目录")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !strings.EqualFold(filepath.Dir(resolved), parent) {
		return errors.New("拒绝清理安装临时目录之外的路径")
	}
	for attempt := 0; ; attempt++ {
		// Remove our payload directly before traversing any NSIS scratch files.
		_ = os.Remove(filepath.Join(dir, "setup.exe"))
		err = os.RemoveAll(dir)
		if err == nil || attempt == 10 {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func commandTail(line string) string {
	quoted := false
	for i, char := range line {
		if char == '"' {
			quoted = !quoted
		} else if !quoted && (char == ' ' || char == '\t') {
			return strings.TrimLeft(line[i:], " \t")
		}
	}
	return ""
}

func silent() bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, "/S") {
			return true
		}
	}
	return false
}
