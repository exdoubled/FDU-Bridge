package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

type File struct {
	Source, Target, SHA256 string
}

type Pending struct {
	Root, Executable, Version string
	ParentPID                 int
	Files                     []File
}

type installStartup struct {
	pending     *Pending
	pid         int
	done        chan struct{}
	result      error
	confirmOnce sync.Once
	confirmErr  error
}

var startupMu sync.Mutex
var startupState *installStartup

func currentInstallStartup() *installStartup {
	startupMu.Lock()
	defer startupMu.Unlock()
	return startupState
}

// StartupPending keeps network and tool actions blocked until the helper commits.
func StartupPending() bool {
	state := currentInstallStartup()
	if state == nil {
		return false
	}
	select {
	case <-state.done:
		return state.result != nil
	default:
		return true
	}
}

// ConfirmStartup is called only after the new GUI's frontend has rendered.
func ConfirmStartup() error {
	state := currentInstallStartup()
	if state == nil {
		return nil
	}
	select {
	case <-state.done:
		return state.result
	default:
	}
	state.confirmOnce.Do(func() {
		state.confirmErr = writeStartupMarker(state.pending, "startup-ready", strconv.Itoa(state.pid))
	})
	return state.confirmErr
}

// AwaitStartup returns immediately for ordinary launches and waits for update commit.
func AwaitStartup() error {
	state := currentInstallStartup()
	if state == nil {
		return nil
	}
	<-state.done
	return state.result
}

// Digest hashes a file's bytes or a sorted directory tree's names, modes and bytes.
func Digest(path string) (string, error) {
	if err := noInstallLinks(path, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if info.Mode().IsRegular() {
		err = hashInstallFile(hash, path)
	} else if info.IsDir() {
		err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("update contains a link or special file: %s", name)
			}
			relative, err := filepath.Rel(path, name)
			if err != nil {
				return err
			}
			size := int64(0)
			if info.Mode().IsRegular() {
				size = info.Size()
			}
			fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", filepath.ToSlash(relative), info.Mode(), size)
			if info.Mode().IsRegular() {
				return hashInstallFile(hash, name)
			}
			return nil
		})
	} else {
		err = fmt.Errorf("update is not a regular file or directory: %s", path)
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashInstallFile(writer io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, f)
	return errors.Join(copyErr, f.Close())
}

func installationRoot(executable string) (string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", errors.New("update executable path must be absolute and clean")
	}
	if runtime.GOOS == "darwin" {
		macos := filepath.Dir(executable)
		contents := filepath.Dir(macos)
		bundle := filepath.Dir(contents)
		if filepath.Base(executable) != "FDU Connect" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(filepath.Base(bundle), ".app") {
			return "", errors.New("updates require a standard macOS app bundle")
		}
		return bundle, nil
	}
	want := "fdu-connect-gui"
	if runtime.GOOS == "windows" {
		want = "FDU-Connect-GUI.exe"
	} else if runtime.GOOS != "linux" {
		return "", errors.New("updates are unsupported on this platform")
	}
	if filepath.Base(executable) != want {
		return "", errors.New("updates require the original GUI executable name")
	}
	return filepath.Dir(executable), nil
}

func validateStageRoot(p *Pending) (string, error) {
	if p == nil {
		return "", errors.New("missing pending update")
	}
	install, err := installationRoot(p.Executable)
	if err != nil {
		return "", err
	}
	name := filepath.Base(p.Root)
	if !filepath.IsAbs(p.Root) || filepath.Clean(p.Root) != p.Root || !strings.HasPrefix(name, ".fdu-update-") || len(name) <= len(".fdu-update-") || filepath.Dir(p.Root) != filepath.Dir(install) || sameInstallPath(p.Root, install) {
		return "", errors.New("update staging directory must be beside the installation")
	}
	if err := noInstallLinks(p.Root, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(p.Root)
	if err != nil || !info.IsDir() {
		return "", errors.New("update staging directory is missing")
	}
	return install, nil
}

// RemoveStage only removes the validated temporary sibling of this installation.
func RemoveStage(p *Pending) error {
	if _, err := validateStageRoot(p); err != nil {
		return err
	}
	return os.RemoveAll(p.Root)
}

// Validate restricts a job to the expected public files in this installation.
func Validate(p *Pending) error {
	install, err := validateStageRoot(p)
	if err != nil {
		return err
	}
	if err := noInstallLinks(p.Executable, false); err != nil {
		return err
	}
	info, err := os.Lstat(p.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("original GUI executable is missing")
	}
	payload := filepath.Join(p.Root, "payload")
	seen := make(map[string]bool)
	gui, core := false, false
	for _, file := range p.Files {
		if !strictInstallChild(payload, file.Source) {
			return errors.New("update source is outside the payload directory")
		}
		key := file.Target
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return errors.New("update contains duplicate targets")
		}
		seen[key] = true
		if runtime.GOOS == "darwin" {
			if len(p.Files) != 1 || file.Target != install || file.Source != filepath.Join(payload, "FDU Connect.app") {
				return errors.New("macOS updates must replace the complete app bundle")
			}
			for _, name := range []string{"FDU Connect", "fdu-connect"} {
				path := filepath.Join(file.Source, "Contents", "MacOS", name)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					return errors.New("update bundle is missing the GUI or core")
				}
			}
			gui, core = true, true
		} else {
			name := filepath.Base(file.Target)
			if !publicFile(name) || file.Target != filepath.Join(install, name) {
				return errors.New("update target is not an allowed public program file")
			}
			info, err := os.Lstat(file.Source)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("update payload must contain regular files")
			}
			gui = gui || name == filepath.Base(p.Executable)
			core = core || name == "fdu-connect" || name == "fdu-connect.exe"
		}
		if err := noInstallLinks(file.Target, true); err != nil {
			return err
		}
		if target, err := os.Lstat(file.Target); err == nil {
			if runtime.GOOS == "darwin" && !target.IsDir() || runtime.GOOS != "darwin" && !target.Mode().IsRegular() {
				return errors.New("existing update target has an unexpected file type")
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(file.SHA256) != 64 {
			return errors.New("update payload has no valid SHA256")
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return errors.New("update payload has no valid SHA256")
		}
		digest, err := Digest(file.Source)
		if err != nil {
			return err
		}
		if digest != strings.ToLower(file.SHA256) {
			return errors.New("update payload changed after download")
		}
	}
	if !gui || !core {
		return errors.New("update must replace both the GUI and core")
	}
	return nil
}

func strictInstallChild(root, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func sameInstallPath(first, second string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

// Reject links in ancestors as well as the final component, including junctions.
func noInstallLinks(path string, allowMissingLeaf bool) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			if !(allowMissingLeaf && current == absolute && errors.Is(err, fs.ErrNotExist)) {
				return err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("update paths cannot contain links: %s", current)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

// Apply changes only public files. Backups stay in staging until relaunch succeeds.
func Apply(p *Pending) error {
	_, err := apply(p, os.Rename)
	return err
}

func apply(p *Pending, rename func(string, string) error) (func() error, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	backup := filepath.Join(p.Root, "backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		return nil, err
	}
	type movedFile struct {
		file               File
		backup             string
		oldMoved, newMoved bool
	}
	var moved []*movedFile
	rollback := func() error {
		var failures []error
		for i := len(moved) - 1; i >= 0; i-- {
			entry := moved[i]
			if entry.newMoved {
				if err := rename(entry.file.Target, entry.file.Source); err != nil {
					failures = append(failures, fmt.Errorf("restore payload %s: %w", entry.file.Target, err))
					continue
				}
				entry.newMoved = false
			}
			if entry.oldMoved {
				if err := rename(entry.backup, entry.file.Target); err != nil {
					failures = append(failures, fmt.Errorf("restore original %s: %w", entry.file.Target, err))
				} else {
					entry.oldMoved = false
				}
			}
		}
		if len(failures) == 0 {
			_ = os.Remove(backup)
		}
		return errors.Join(failures...)
	}
	for i, file := range p.Files {
		entry := &movedFile{file: file, backup: filepath.Join(backup, fmt.Sprintf("%d", i))}
		moved = append(moved, entry)
		if _, err := os.Lstat(file.Target); err == nil {
			if err := rename(file.Target, entry.backup); err != nil {
				return rollback, errors.Join(err, rollback())
			}
			entry.oldMoved = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return rollback, errors.Join(err, rollback())
		}
		if err := rename(file.Source, file.Target); err != nil {
			return rollback, errors.Join(err, rollback())
		}
		entry.newMoved = true
	}
	return rollback, nil
}

// StartHelper prepares an isolated helper and waits until it has validated the job.
func StartHelper(p *Pending) (func(), error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	current, err := os.Executable()
	if err != nil || !sameInstallPath(current, p.Executable) {
		return nil, errors.New("only the installed GUI can start its update helper")
	}
	p.ParentPID = os.Getpid()
	helper := filepath.Join(p.Root, helperName())
	ready := filepath.Join(p.Root, "ready")
	if _, err := os.Lstat(ready); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("update helper readiness marker already exists")
	}
	if err := copyInstallHelper(current, helper); err != nil {
		return nil, err
	}
	job := filepath.Join(p.Root, "job.json")
	jobCreated, started, handedOff := false, false, false
	cleanup := func() {
		_ = os.Remove(helper)
		if jobCreated {
			_ = os.Remove(job)
		}
		if started {
			_ = os.Remove(ready)
		}
	}
	defer func() {
		if !handedOff {
			cleanup()
		}
	}()
	data, err := json.Marshal(p)
	if err == nil {
		err = writeInstallExclusive(job, data, 0o600)
	}
	if err != nil {
		return nil, err
	}
	jobCreated = true
	cmd := exec.Command(helper, "--fdu-update-helper", job)
	cmd.Dir = p.Root
	detachInstallCommand(cmd, true)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	started = true
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			cleanup()
		})
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return nil, fmt.Errorf("update helper exited before becoming ready: %v", err)
		case <-timer.C:
			cancel()
			return nil, errors.New("update helper did not become ready within five seconds")
		case <-ticker.C:
			if marker, err := readStartupMarker(p, "ready"); err == nil && marker == "ready" {
				handedOff = true
				return cancel, nil
			}
		}
	}
}

func helperName() string {
	if runtime.GOOS == "windows" {
		return "helper.exe"
	}
	return "helper"
}

func copyInstallHelper(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	err = errors.Join(copyErr, output.Close())
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}

func writeInstallExclusive(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// RunHelper runs before Wails initialization and reports whether this is a helper.
func RunHelper() bool {
	if len(os.Args) < 2 {
		return false
	}
	if os.Args[1] == "--fdu-update-cleanup" {
		state := &installStartup{pid: os.Getpid(), done: make(chan struct{}), result: errors.New("invalid update startup arguments")}
		if len(os.Args) == 3 {
			executable, err := os.Executable()
			if err == nil {
				state.pending, err = cleanupInstallJob(os.Args[2], executable)
			}
			state.result = err
		}
		startupMu.Lock()
		startupState = state
		startupMu.Unlock()
		if state.result != nil {
			close(state.done)
		} else {
			go watchInstallCommit(state)
		}
		os.Args = os.Args[:1]
		return false
	}
	if os.Args[1] != "--fdu-update-helper" {
		return false
	}
	if len(os.Args) == 3 {
		_ = runInstallHelper(os.Args[2])
	}
	return true
}

func readInstallJob(job string) (*Pending, error) {
	if err := noInstallLinks(job, false); err != nil {
		return nil, err
	}
	info, err := os.Lstat(job)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("update job must be a bounded regular file")
	}
	f, err := os.Open(job)
	if err != nil {
		return nil, err
	}
	var p Pending
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&p)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("update job contains trailing data")
		}
	}
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func cleanupInstallJob(root, executable string) (*Pending, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("cleanup requires an absolute staging directory")
	}
	p, err := readInstallJob(filepath.Join(root, "job.json"))
	if err != nil {
		return nil, err
	}
	if p.Root != root || !sameInstallPath(p.Executable, executable) {
		return nil, errors.New("cleanup job does not belong to this executable")
	}
	if _, err := validateStageRoot(p); err != nil {
		return nil, err
	}
	return p, nil
}

func writeStartupMarker(p *Pending, name, value string) error {
	if _, err := validateStageRoot(p); err != nil {
		return err
	}
	target := filepath.Join(p.Root, name)
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("update startup marker already exists")
	}
	f, err := os.CreateTemp(p.Root, "."+name+"-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := io.WriteString(f, value)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}

func readStartupMarker(p *Pending, name string) (string, error) {
	path := filepath.Join(p.Root, name)
	if err := noInstallLinks(path, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 128 {
		return "", errors.New("update startup marker must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 129))
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return "", err
	}
	if len(data) > 128 {
		return "", errors.New("update startup marker is too large")
	}
	return string(data), nil
}

func waitStartupCommit(p *Pending, pid int, timeout time.Duration) (int, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		marker, err := readStartupMarker(p, "committed")
		if err == nil {
			parts := strings.Fields(marker)
			if len(parts) == 2 {
				guiPID, guiErr := strconv.Atoi(parts[0])
				helperPID, helperErr := strconv.Atoi(parts[1])
				if guiErr == nil && helperErr == nil && guiPID == pid && helperPID > 1 && helperPID != pid {
					return helperPID, nil
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
		select {
		case <-timer.C:
			return 0, errors.New("update helper did not commit the new GUI startup")
		case <-ticker.C:
		}
	}
}

func watchInstallCommit(state *installStartup) {
	helperPID, err := waitStartupCommit(state.pending, state.pid, 20*time.Second)
	state.result = err
	close(state.done)
	if err != nil {
		return
	}
	wait, closeHelper, err := openInstallParent(helperPID)
	if err != nil {
		return
	}
	err = wait(5 * time.Second)
	closeHelper()
	if err != nil {
		return
	}
	for attempt := 0; attempt < 10; attempt++ {
		if err := RemoveStage(state.pending); err == nil || errors.Is(err, fs.ErrNotExist) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type installChild struct {
	pid   int
	done  <-chan struct{}
	alive func() bool
	stop  func() error
}

func startInstallChild(executable, root string) (*installChild, error) {
	cmd := exec.Command(executable, "--fdu-update-cleanup", root)
	cmd.Dir = filepath.Dir(executable)
	detachInstallCommand(cmd, false)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	child := &installChild{pid: cmd.Process.Pid, done: done}
	child.alive = func() bool {
		select {
		case <-done:
			return false
		default:
			_, err := sysproc.StartTime(uint32(child.pid))
			return err == nil
		}
	}
	child.stop = func() error {
		select {
		case <-done:
			return nil
		default:
		}
		killErr := cmd.Process.Kill()
		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			return errors.Join(killErr, errors.New("new GUI did not exit; rollback backups have been retained"))
		}
	}
	return child, nil
}

func commitNewGUI(p *Pending, child *installChild, timeout time.Duration) error {
	err := awaitNewGUI(p, child, timeout)
	if err == nil {
		err = writeStartupMarker(p, "committed", fmt.Sprintf("%d %d", child.pid, os.Getpid()))
	}
	if err != nil {
		return errors.Join(err, child.stop())
	}
	return nil
}

func awaitNewGUI(p *Pending, child *installChild, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-child.done:
			return errors.New("new GUI exited before confirming startup")
		default:
		}
		marker, err := readStartupMarker(p, "startup-ready")
		if err == nil && marker == strconv.Itoa(child.pid) {
			if !child.alive() {
				return errors.New("new GUI is no longer running")
			}
			return nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		select {
		case <-child.done:
			return errors.New("new GUI exited before confirming startup")
		case <-timer.C:
			return errors.New("new GUI did not confirm startup before timeout")
		case <-ticker.C:
		}
	}
}

func checkInstallCores(entries []sysproc.Entry, listErr error) error {
	if listErr != nil {
		return fmt.Errorf("cannot verify that FDU cores are stopped: %w", listErr)
	}
	for _, entry := range entries {
		for _, name := range sysproc.CoreNames {
			if strings.EqualFold(entry.Name, name) {
				return errors.New("an FDU core is still running; update was not applied")
			}
		}
	}
	return nil
}

func runInstallHelper(job string) (helperErr error) {
	p, err := readInstallJob(job)
	if err != nil {
		return err
	}
	defer func() {
		if helperErr != nil {
			writeInstallResult(p, helperErr)
		}
	}()
	if err := Validate(p); err != nil {
		return err
	}
	current, err := os.Executable()
	if err != nil || !sameInstallPath(current, filepath.Join(p.Root, helperName())) || job != filepath.Join(p.Root, "job.json") || p.ParentPID <= 1 || p.ParentPID == os.Getpid() {
		return errors.New("invalid update helper identity")
	}
	wait, closeParent, err := openInstallParent(p.ParentPID)
	if err != nil {
		return err
	}
	defer closeParent()
	if err := writeInstallExclusive(filepath.Join(p.Root, "ready"), []byte("ready"), 0o600); err != nil {
		return err
	}
	if err := wait(30 * time.Second); err != nil {
		return err
	}
	if err := checkInstallCores(sysproc.List()); err != nil {
		return errors.Join(err, relaunchInstall(p.Executable, ""))
	}
	rollback, applyErr := apply(p, os.Rename)
	if applyErr != nil {
		if rollback != nil {
			if restoreErr := rollback(); restoreErr != nil {
				return errors.Join(applyErr, restoreErr)
			}
		}
		return errors.Join(applyErr, relaunchInstall(p.Executable, ""))
	}
	child, startErr := startInstallChild(p.Executable, p.Root)
	if startErr == nil {
		startErr = commitNewGUI(p, child, 15*time.Second)
		if startErr != nil {
			select {
			case <-child.done:
			default:
				// Never replace files while the new GUI could still be using them.
				return startErr
			}
		}
	}
	if startErr != nil {
		restoreErr := rollback()
		if restoreErr == nil {
			return errors.Join(startErr, relaunchInstall(p.Executable, ""))
		}
		return errors.Join(startErr, restoreErr)
	}
	// The new GUI removes staging after this helper exits (required on Windows).
	return nil
}

func relaunchInstall(executable, cleanupRoot string) error {
	var args []string
	if cleanupRoot != "" {
		args = []string{"--fdu-update-cleanup", cleanupRoot}
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = filepath.Dir(executable)
	detachInstallCommand(cmd, false)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func writeInstallResult(p *Pending, result error) {
	if _, err := validateStageRoot(p); err != nil {
		return
	}
	_ = writeInstallExclusive(filepath.Join(p.Root, "result.log"), []byte(result.Error()+"\n"), 0o600)
}
