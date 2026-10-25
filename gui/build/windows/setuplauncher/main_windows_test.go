package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func setupArchive(t *testing.T, name string, data []byte) string {
	return setupArchiveMethod(t, name, data, zip.Store)
}

func setupArchiveMethod(t *testing.T, name string, data []byte, method uint16) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "setup.exe")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// The distributed file has a native PE launcher before its ZIP overlay.
	_, _ = f.WriteString("MZ launcher prefix")
	z := zip.NewWriter(f)
	w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareSetupReadsDeflatedOverlay(t *testing.T) {
	// .NET Framework encodes NoCompression as a deflate stream, unlike modern .NET.
	archive := setupArchiveMethod(t, "setup.exe", []byte("NSIS payload"), zip.Deflate)
	dir, path, err := prepareSetup(archive, []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorkDir(dir)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "NSIS payload" {
		t.Fatalf("payload = %q, %v", data, err)
	}
}

func TestPrepareSetupSkipsUnusableTempDirectory(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "not a directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := setupArchive(t, "setup.exe", []byte("NSIS payload"))
	dir, executable, err := prepareSetup(archive, []string{blocked, root})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if filepath.Dir(dir) != root {
		t.Fatalf("did not use writable fallback: %s", dir)
	}
	data, err := os.ReadFile(executable)
	if err != nil || string(data) != "NSIS payload" {
		t.Fatalf("extracted payload: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "setup.exe" {
		t.Fatalf("write probe was not removed: %v, %v", entries, err)
	}
}

func TestPrepareSetupReportsAllUnusableDirectories(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := prepareSetup(setupArchive(t, "setup.exe", []byte("payload")), []string{blocked})
	if err == nil || !strings.Contains(err.Error(), blocked) {
		t.Fatalf("missing failed directory and system error: %v", err)
	}
}

func TestPrepareSetupRejectsUnexpectedPayload(t *testing.T) {
	root := t.TempDir()
	if _, _, err := prepareSetup(setupArchive(t, "../setup.exe", nil), []string{root}); err == nil {
		t.Fatal("unsafe payload accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid payload created temporary files: %v, %v", entries, err)
	}
}

func TestNSISWriteProbeUsesDeleteOnClose(t *testing.T) {
	dir := t.TempDir()
	if err := probeTemp(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe left files: %v, %v", entries, err)
	}
}

func TestRemoveWorkDirWaitsForTransientFileLock(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), "fdu-setup-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "setup.exe")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(100*time.Millisecond, func() { _ = syscall.CloseHandle(handle) })
	defer func() {
		if timer.Stop() {
			_ = syscall.CloseHandle(handle)
		}
	}()
	if err := removeWorkDir(dir); err != nil {
		t.Fatalf("temporary reader prevented cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory was not removed: %v", err)
	}
}

func TestCommandTailPreservesNSISDirectorySyntax(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"C:\Downloads\setup.exe" /S /D=C:\用户\安装 目录`, `/S /D=C:\用户\安装 目录`},
		{`C:\setup.exe /S /D=D:\Applications\FDU Connect`, `/S /D=D:\Applications\FDU Connect`},
		{`"C:\setup folder\setup.exe"`, ""},
	} {
		if got := commandTail(tc.input); got != tc.want {
			t.Fatalf("command tail = %q, want %q", got, tc.want)
		}
	}
}

func TestRunSetupIsolatesEnvironmentForwardsArgumentsAndCleansUp(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	archive := setupArchive(t, "setup.exe", data)
	root := t.TempDir()
	result := filepath.Join(root, "result.json")
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FDU_SETUP_TEST_CHILD", result)
	t.Setenv("TEMP", blocked)
	t.Setenv("TMP", blocked)
	tail := `-test.run=^TestSetupChild$ -- /S /D=C:\用户\FDU Connect`
	code, err := runSetup(archive, tail, []string{blocked, root})
	if err != nil || code != 17 {
		t.Fatalf("child exit = %d, %v", code, err)
	}
	data, err = os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Temp, TMP, Command string }
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Temp == blocked || got.Temp != got.TMP || !strings.HasSuffix(got.Command, tail) {
		t.Fatalf("child environment or command line changed: %+v", got)
	}
	if _, err := os.Stat(got.Temp); !os.IsNotExist(err) {
		t.Fatalf("temporary installation folder retained: %v", err)
	}
	if os.Getenv("TEMP") != blocked || os.Getenv("TMP") != blocked {
		t.Fatal("launch modified the parent environment")
	}
}

func TestSetupChild(t *testing.T) {
	path := os.Getenv("FDU_SETUP_TEST_CHILD")
	if path == "" {
		return
	}
	data, err := json.Marshal(struct{ Temp, TMP, Command string }{os.Getenv("TEMP"), os.Getenv("TMP"), rawCommandLine()})
	if err != nil {
		os.Exit(91)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		os.Exit(92)
	}
	os.Exit(17)
}
