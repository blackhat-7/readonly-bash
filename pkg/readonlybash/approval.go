package readonlybash

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const defaultApprovalTTL = 30 * time.Minute

var (
	ErrApprovalPending = errors.New("approval already pending")
	ErrNoApproval      = errors.New("no matching approval")
)

type Approval struct {
	ID              string    `json:"id"`
	RequestID       string    `json:"requestID"`
	CanonicalCwd    string    `json:"canonicalCwd"`
	OriginalCommand string    `json:"originalCommand"`
	CommandToRun    string    `json:"commandToRun"`
	CreatedAt       time.Time `json:"createdAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type CreateApprovalOptions struct {
	ApprovalDir     string
	RequestID       string
	Cwd             string
	OriginalCommand string
	CommandToRun    string
	TTL             time.Duration
}

type ClaimApprovalOptions struct {
	ApprovalDir string
	Cwd         string
}

func CanonicalCwd(path string) (string, error) {
	if path == "" {
		return "", errors.New("cwd is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func CreateApproval(opts CreateApprovalOptions) (*Approval, error) {
	if opts.ApprovalDir == "" || opts.RequestID == "" || opts.OriginalCommand == "" || opts.CommandToRun == "" {
		return nil, errors.New("approval dir, request id, original command, and command to run are required")
	}
	canonicalCwd, err := CanonicalCwd(opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("canonicalize cwd: %w", err)
	}
	if opts.TTL <= 0 {
		opts.TTL = defaultApprovalTTL
	}

	store, err := lockStore(opts.ApprovalDir)
	if err != nil {
		return nil, err
	}
	defer store.close()

	now := time.Now().UTC()
	if err := store.cleanupExpired(now); err != nil {
		return nil, err
	}
	approvals, err := store.listApprovals()
	if err != nil {
		return nil, err
	}
	if len(approvals) > 0 {
		return nil, ErrApprovalPending
	}

	id, err := newApprovalID()
	if err != nil {
		return nil, err
	}
	approval := &Approval{
		ID:              id,
		RequestID:       opts.RequestID,
		CanonicalCwd:    canonicalCwd,
		OriginalCommand: opts.OriginalCommand,
		CommandToRun:    opts.CommandToRun,
		CreatedAt:       now,
		ExpiresAt:       now.Add(opts.TTL),
	}
	return approval, store.writeApproval(approval)
}

func ClaimApproval(opts ClaimApprovalOptions) (*Approval, error) {
	if opts.ApprovalDir == "" {
		return nil, errors.New("approval dir is required")
	}
	canonicalCwd, err := CanonicalCwd(opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("canonicalize cwd: %w", err)
	}

	store, err := lockStore(opts.ApprovalDir)
	if err != nil {
		return nil, err
	}
	defer store.close()

	if err := store.cleanupExpired(time.Now().UTC()); err != nil {
		return nil, err
	}
	approvals, err := store.listApprovals()
	if err != nil {
		return nil, err
	}
	for _, approval := range approvals {
		if approval.CanonicalCwd != canonicalCwd {
			continue
		}
		if err := os.Remove(store.path(approval.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return approval, nil
	}
	return nil, ErrNoApproval
}

type approvalStore struct {
	dir      string
	lockFile *os.File
}

func lockStore(dir string) (*approvalStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "store.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	return &approvalStore{dir: dir, lockFile: lockFile}, nil
}

func (s *approvalStore) close() {
	_ = syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
	_ = s.lockFile.Close()
}

func (s *approvalStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *approvalStore) listApprovals() ([]*Approval, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var approvals []*Approval
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		approval, err := s.readApproval(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, nil
}

func (s *approvalStore) cleanupExpired(now time.Time) error {
	approvals, err := s.listApprovals()
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if approval.ExpiresAt.After(now) {
			continue
		}
		if err := os.Remove(s.path(approval.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *approvalStore) readApproval(path string) (*Approval, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var approval Approval
	if err := json.Unmarshal(data, &approval); err != nil {
		return nil, err
	}
	if approval.ID == "" || approval.CanonicalCwd == "" || approval.CommandToRun == "" {
		return nil, errors.New("invalid approval file")
	}
	return &approval, nil
}

func (s *approvalStore) writeApproval(approval *Approval) error {
	data, err := json.MarshalIndent(approval, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "."+approval.ID+".tmp")
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(tmp)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path(approval.ID))
}

func newApprovalID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
